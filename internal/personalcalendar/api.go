package personalcalendar

import (
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// request allows retries only for read-only GETs. OAuth POSTs are not blindly
// repeated after uncertain transport outcomes. Error bodies are never returned.
func (b *Broker) request(ctx context.Context, method, endpoint, token string, form map[string]string, retry bool) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	stop := context.AfterFunc(b.lifetime, cancel)
	defer stop()
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		if e := b.alive(ctx); e != nil {
			return 0, nil, e
		}
		var body io.Reader
		if form != nil {
			values := url.Values{}
			for k, v := range form {
				values.Set(k, v)
			}
			body = strings.NewReader(values.Encode())
		}
		req, e := http.NewRequestWithContext(ctx, method, endpoint, body)
		if e != nil {
			return 0, nil, fault("invalid_request")
		}
		req.Header.Set("Accept", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		resp, e := b.client.Do(req)
		delay := time.Duration(250*(1<<attempt)+rand.IntN(200)) * time.Millisecond
		status := 0
		var data []byte
		transient := e != nil
		if resp != nil {
			status = resp.StatusCode
			data, e = io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
			_ = resp.Body.Close()
			if e != nil || len(data) > 2*1024*1024 {
				return status, nil, fault("invalid_response")
			}
			transient = status == 429 || status >= 500
			if status == 403 {
				var problem struct {
					Error struct {
						Errors []struct {
							Reason string `json:"reason"`
						} `json:"errors"`
					} `json:"error"`
				}
				_ = json.Unmarshal(data, &problem)
				for _, item := range problem.Error.Errors {
					if item.Reason == "rateLimitExceeded" || item.Reason == "userRateLimitExceeded" {
						transient = true
					}
				}
			}
			if header := resp.Header.Get("Retry-After"); header != "" {
				if seconds, err := strconv.Atoi(header); err == nil && seconds >= 0 {
					delay = time.Duration(seconds) * time.Second
				} else if until, err := http.ParseTime(header); err == nil {
					delay = until.Sub(b.now())
				}
			}
		}
		if ctx.Err() != nil {
			return status, nil, fault("request_cancelled")
		}
		if !transient {
			if e != nil {
				return status, nil, fault("provider_unavailable")
			}
			return status, data, nil
		}
		if !retry || method != "GET" || attempt == 2 || delay > 5*time.Second {
			return status, nil, fault("provider_unavailable")
		}
		if delay < 0 {
			delay = 0
		}
		if e = b.wait(ctx, delay); e != nil {
			return status, nil, e
		}
	}
	return 0, nil, fault("provider_unavailable")
}

type Calendar struct {
	ID         string `json:"id"`
	Summary    string `json:"summary"`
	TimeZone   string `json:"timeZone"`
	Primary    bool   `json:"primary"`
	AccessRole string `json:"accessRole"`
}

func (b *Broker) calendars(ctx context.Context, token string) ([]Calendar, error) {
	items := []Calendar{}
	seen := map[string]bool{}
	ids := map[string]bool{}
	page := ""
	for i := 0; i < 10; i++ {
		u, _ := url.Parse("https://www.googleapis.com/calendar/v3/users/me/calendarList")
		q := url.Values{"minAccessRole": {"owner"}, "maxResults": {"250"}, "showDeleted": {"false"}, "fields": {"items(id,summary,timeZone,primary,accessRole),nextPageToken"}}
		if page != "" {
			q.Set("pageToken", page)
		}
		u.RawQuery = q.Encode()
		status, body, e := b.request(ctx, "GET", u.String(), token, nil, true)
		if e != nil {
			return nil, e
		}
		if status == 401 || status == 403 {
			return nil, fault("authorization_denied")
		}
		if status != 200 {
			return nil, fault("provider_unavailable")
		}
		var result struct {
			Items []Calendar `json:"items"`
			Next  string     `json:"nextPageToken"`
		}
		if json.Unmarshal(body, &result) != nil {
			return nil, fault("invalid_response")
		}
		for _, c := range result.Items {
			if c.AccessRole != "owner" || c.ID == "" || len(c.ID) > 512 || len(c.Summary) > 8192 {
				return nil, fault("invalid_response")
			}
			if _, e = time.LoadLocation(c.TimeZone); e != nil {
				return nil, fault("invalid_response")
			}
			if !ids[c.ID] {
				items = append(items, c)
				ids[c.ID] = true
			}
			if len(items) > 1000 {
				return nil, fault("result_limit")
			}
		}
		if result.Next == "" {
			return items, nil
		}
		if seen[result.Next] || len(result.Next) > 4096 {
			return nil, fault("invalid_response")
		}
		seen[result.Next] = true
		page = result.Next
	}
	return nil, fault("page_limit")
}
func (b *Broker) Calendars(ctx context.Context, a Actor, id string) ([]Calendar, error) {
	r, e := b.grant(ctx, a, id)
	if e != nil {
		return nil, e
	}
	result, e := b.calendars(ctx, r.AccessToken)
	if e != nil {
		return nil, b.authorizationFailure(ctx, a, r, e)
	}
	if e = b.check(ctx, a, r); e != nil {
		return nil, e
	}
	return result, nil
}
func (b *Broker) Select(ctx context.Context, a Actor, id string, ids []string) error {
	if len(ids) > 8 {
		return fault("calendar_selection_limit")
	}
	list, e := b.Calendars(ctx, a, id)
	if e != nil {
		return e
	}
	for _, id := range ids {
		if !slices.ContainsFunc(list, func(c Calendar) bool { return c.ID == id }) {
			return fault("calendar_not_owned")
		}
	}
	selected := slices.Clone(ids)
	slices.Sort(selected)
	selected = slices.Compact(selected)
	return b.change(ctx, a, id, func(r *stored) error {
		if r.Connection.State != "connected" {
			return fault("reconnect_required")
		}
		r.Connection.SelectedCalendars = selected
		r.Connection.SelectionRevision = randomID()
		r.Revision = randomID()
		return nil
	})
}

type EventQuery struct {
	CalendarID string `json:"calendar_id"`
	TimeMin    string `json:"time_min"`
	TimeMax    string `json:"time_max"`
	TimeZone   string `json:"timezone"`
	PageToken  string `json:"page_token,omitempty"`
}

func (q EventQuery) validate() error {
	start, e := time.Parse(time.RFC3339, q.TimeMin)
	if e != nil {
		return fault("invalid_time_range")
	}
	end, e := time.Parse(time.RFC3339, q.TimeMax)
	if e != nil || !end.After(start) || end.Sub(start) > 31*24*time.Hour {
		return fault("invalid_time_range")
	}
	if _, e = time.LoadLocation(q.TimeZone); e != nil || q.TimeZone == "" || len(q.TimeZone) > 100 {
		return fault("invalid_timezone")
	}
	if q.CalendarID == "" || len(q.CalendarID) > 512 || strings.ContainsAny(q.CalendarID, "/\\\x00\r\n") || len(q.PageToken) > 4096 {
		return fault("invalid_query")
	}
	return nil
}

// EventsPage is a narrow host service: fixed Google URL, GET, selected owned
// calendar, explicit <=31-day window. No generic URL or credential RPC is exposed.
func (b *Broker) EventsPage(ctx context.Context, a Actor, id string, q EventQuery) (json.RawMessage, error) {
	if e := q.validate(); e != nil {
		return nil, e
	}
	r, e := b.grant(ctx, a, id)
	if e != nil {
		return nil, e
	}
	if !slices.Contains(r.Connection.SelectedCalendars, q.CalendarID) {
		return nil, fault("calendar_not_selected")
	}
	u, _ := url.Parse("https://www.googleapis.com/calendar/v3/calendars/" + url.PathEscape(q.CalendarID) + "/events")
	params := url.Values{"timeMin": {q.TimeMin}, "timeMax": {q.TimeMax}, "timeZone": {q.TimeZone}, "singleEvents": {"true"}, "showDeleted": {"true"}, "maxResults": {"250"}, "orderBy": {"startTime"}, "fields": {"timeZone,nextPageToken,items(id,etag,status,summary,location,start,end,recurringEventId,originalStartTime,updated)"}}
	if q.PageToken != "" {
		params.Set("pageToken", q.PageToken)
	}
	u.RawQuery = params.Encode()
	status, body, e := b.request(ctx, "GET", u.String(), r.AccessToken, nil, true)
	if e != nil {
		return nil, e
	}
	if status == 401 || status == 403 {
		e = fault("authorization_denied")
		return nil, b.authorizationFailure(ctx, a, r, e)
	}
	if status != 200 {
		return nil, fault("provider_unavailable")
	}
	if !json.Valid(body) {
		return nil, fault("invalid_response")
	}
	if e = b.check(ctx, a, r); e != nil {
		return nil, e
	}
	return json.RawMessage(body), nil
}
func (b *Broker) authorizationFailure(ctx context.Context, a Actor, r stored, e error) error {
	if !authorizationError(e) {
		return e
	}
	b.deny(r.Connection.ID, e)
	commit := b.change(ctx, a, r.Connection.ID, func(current *stored) error {
		if current.Revision != r.Revision {
			return fault("connection_changed")
		}
		current.Connection.State = "needs_reauth"
		current.Connection.ErrorCode = "authorization_denied"
		current.AccessToken = ""
		current.RefreshToken = ""
		current.Revision = randomID()
		return nil
	})
	if commit != nil {
		return &Failure{Code: e.(*Failure).Code, PersistenceFailed: true}
	}
	return e
}
