package personalconnectors

import (
	"encoding/json"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"time"
)

type calendarInput struct {
	CalendarID  string   `json:"calendar_id"`
	EventID     string   `json:"event_id,omitempty"`
	ETag        string   `json:"etag,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Start       string   `json:"start,omitempty"`
	End         string   `json:"end,omitempty"`
	Timezone    string   `json:"timezone,omitempty"`
	Attendees   []string `json:"attendees,omitempty"`
	SendUpdates string   `json:"send_updates"`
	TimeMin     string   `json:"time_min,omitempty"`
	TimeMax     string   `json:"time_max,omitempty"`
	PageToken   string   `json:"page_token,omitempty"`
}
type calendarResponse struct {
	ID               string `json:"id"`
	ETag             string `json:"etag"`
	Status           string `json:"status"`
	Summary          string `json:"summary"`
	Transparency     string `json:"transparency"`
	AttendeesOmitted bool   `json:"attendeesOmitted"`
	Start            struct {
		DateTime string `json:"dateTime"`
		TimeZone string `json:"timeZone"`
	} `json:"start"`
	End struct {
		DateTime string `json:"dateTime"`
		TimeZone string `json:"timeZone"`
	} `json:"end"`
	Attendees []struct {
		Email string `json:"email"`
	} `json:"attendees"`
}

func calendarIdentifier(v string) bool {
	return v != "" && len(v) <= 512 && !strings.ContainsAny(v, "/\\\r\n\x00") && v != "." && v != ".."
}
func calendarETag(v string) bool {
	return len(v) >= 3 && len(v) <= 512 && v[0] == '"' && v[len(v)-1] == '"' && !strings.ContainsAny(v[1:len(v)-1], "\"\r\n\x00")
}
func calendarReadScopes(c Connection) []string {
	for _, scope := range []string{"https://www.googleapis.com/auth/calendar.events.readonly", "https://www.googleapis.com/auth/calendar.events", "https://www.googleapis.com/auth/calendar.readonly", "https://www.googleapis.com/auth/calendar"} {
		if slices.Contains(c.Scopes, scope) {
			return []string{scope}
		}
	}
	return []string{"https://www.googleapis.com/auth/calendar.events.readonly"}
}
func buildCalendar(c Connection, op string, raw json.RawMessage) (Plan, error) {
	var in calendarInput
	if err := decode(raw, &in); err != nil {
		return Plan{}, err
	}
	if !calendarIdentifier(in.CalendarID) {
		return Plan{}, fault("calendar_id_required")
	}
	base := "https://www.googleapis.com/calendar/v3/calendars/" + url.PathEscape(in.CalendarID) + "/events"
	p := Plan{Method: "GET", Resource: in.CalendarID, URL: base, RequiredScopes: calendarReadScopes(c)}
	if op == "calendar.agenda" {
		start, e := time.Parse(time.RFC3339, in.TimeMin)
		end, e2 := time.Parse(time.RFC3339, in.TimeMax)
		if e != nil || e2 != nil || !end.After(start) || end.Sub(start) > 31*24*time.Hour || providerCursor(in.PageToken) != nil {
			return Plan{}, fault("invalid_time_range")
		}
		q := url.Values{"timeMin": {in.TimeMin}, "timeMax": {in.TimeMax}, "singleEvents": {"true"}, "maxResults": {"100"}}
		if in.PageToken != "" {
			q.Set("pageToken", in.PageToken)
		}
		p.URL += "?" + q.Encode()
		return p, nil
	}
	if op != "calendar.create" && op != "calendar.reschedule" && op != "calendar.cancel" {
		return Plan{}, fault("unsupported_operation")
	}
	if in.SendUpdates != "all" && in.SendUpdates != "externalOnly" && in.SendUpdates != "none" {
		return Plan{}, fault("explicit_notification_choice_required")
	}
	writeScope := "https://www.googleapis.com/auth/calendar.events"
	if slices.Contains(c.Scopes, "https://www.googleapis.com/auth/calendar") {
		writeScope = "https://www.googleapis.com/auth/calendar"
	}
	p.Write = true
	p.RequiredScopes = []string{writeScope}
	p.Summary = op + " in selected calendar " + in.CalendarID + "; notifications: " + in.SendUpdates
	p.Filter = func(data []byte) (json.RawMessage, error) {
		if op == "calendar.cancel" {
			return providerJSON(map[string]string{"state": "provider_accepted", "notification_delivery": "unverified"}), nil
		}
		var event calendarResponse
		if json.Unmarshal(data, &event) != nil || !calendarIdentifier(event.ID) || !calendarETag(event.ETag) {
			return nil, fault("event_acknowledgement_missing")
		}
		var result map[string]json.RawMessage
		_ = json.Unmarshal(data, &result)
		result["notification_delivery"] = providerJSON("unverified")
		return providerJSON(result), nil
	}
	readPlan := func(endpoint string) Plan {
		return Plan{Method: "GET", URL: endpoint, Resource: in.CalendarID, RequiredScopes: []string{writeScope}}
	}
	var original calendarResponse
	if op != "calendar.create" {
		if !calendarIdentifier(in.EventID) || !calendarETag(in.ETag) {
			return Plan{}, fault("event_etag_required")
		}
		p.URL += "/" + url.PathEscape(in.EventID)
		p.Headers = map[string]string{"If-Match": in.ETag}
		before := readPlan(p.URL)
		before.Filter = func(data []byte) (json.RawMessage, error) {
			if json.Unmarshal(data, &original) != nil || original.ID != in.EventID || original.ETag != in.ETag || (original.Status != "confirmed" && original.Status != "tentative") || original.AttendeesOmitted {
				return nil, fault("event_changed")
			}
			return providerJSON(map[string]string{"id": original.ID, "etag": original.ETag}), nil
		}
		p.Preflight = append(p.Preflight, before)
	}
	p.URL += "?sendUpdates=" + url.QueryEscape(in.SendUpdates)
	if op == "calendar.cancel" {
		p.Method = "DELETE"
		p.Verify = func(json.RawMessage) ([]Plan, error) {
			read := readPlan(base + "/" + url.PathEscape(in.EventID))
			read.AcceptGone = true
			read.Filter = func(data []byte) (json.RawMessage, error) {
				var event calendarResponse
				if json.Unmarshal(data, &event) != nil || event.ID != in.EventID || event.Status != "cancelled" {
					return nil, fault("cancellation_unverified")
				}
				return providerJSON(map[string]string{"id": event.ID, "status": "cancelled", "verification": "read_back_verified", "notification_delivery": "unverified"}), nil
			}
			return []Plan{read}, nil
		}
		return p, nil
	}
	start, e := time.Parse(time.RFC3339, in.Start)
	end, e2 := time.Parse(time.RFC3339, in.End)
	if e != nil || e2 != nil || !end.After(start) || end.Sub(start) > 31*24*time.Hour {
		return Plan{}, fault("invalid_event_time")
	}
	if in.Timezone == "" || len(in.Timezone) > 128 {
		return Plan{}, fault("invalid_timezone")
	}
	if _, e = time.LoadLocation(in.Timezone); e != nil {
		return Plan{}, fault("invalid_timezone")
	}
	if !boundedProviderText(in.Summary, 2048) || len(in.Attendees) > 50 {
		return Plan{}, fault("invalid_event")
	}
	fields := map[string]any{"start": map[string]string{"dateTime": in.Start, "timeZone": in.Timezone}, "end": map[string]string{"dateTime": in.End, "timeZone": in.Timezone}}
	expectedAttendees := []string{}
	if op == "calendar.create" {
		if strings.TrimSpace(in.Summary) == "" || in.EventID != "" || in.ETag != "" {
			return Plan{}, fault("invalid_new_event")
		}
		p.Method = "POST"
		fields["summary"] = in.Summary
		attendees := []map[string]string{}
		for _, email := range in.Attendees {
			a, err := mail.ParseAddress(email)
			if err != nil || a.Address != email || len(email) > 254 || strings.ContainsAny(email, "\r\n") {
				return Plan{}, fault("invalid_attendee")
			}
			email = strings.ToLower(email)
			if slices.Contains(expectedAttendees, email) {
				return Plan{}, fault("duplicate_attendee")
			}
			expectedAttendees = append(expectedAttendees, email)
			attendees = append(attendees, map[string]string{"email": email})
		}
		slices.Sort(expectedAttendees)
		fields["attendees"] = attendees
	} else {
		p.Method = "PATCH"
		if len(in.Attendees) > 0 || in.Summary != "" {
			return Plan{}, fault("reschedule_time_only")
		}
	}
	availability := readPlan(base + "?" + url.Values{"timeMin": {in.Start}, "timeMax": {in.End}, "singleEvents": {"true"}, "showDeleted": {"false"}, "maxResults": {"2500"}}.Encode())
	availability.Filter = func(data []byte) (json.RawMessage, error) {
		var response struct {
			Items         []calendarResponse `json:"items"`
			NextPageToken string             `json:"nextPageToken"`
		}
		if json.Unmarshal(data, &response) != nil || response.Items == nil {
			return nil, fault("invalid_availability_response")
		}
		if response.NextPageToken != "" {
			return nil, fault("availability_incomplete")
		}
		for _, event := range response.Items {
			if !calendarIdentifier(event.ID) {
				return nil, fault("invalid_availability_response")
			}
			if event.ID != in.EventID && event.Status != "cancelled" && event.Transparency != "transparent" {
				return nil, fault("calendar_conflict")
			}
		}
		return providerJSON(map[string]string{"availability": "no_conflict", "scope": "selected_calendar_only"}), nil
	}
	p.Preflight = append(p.Preflight, availability)
	p.Body = providerJSON(fields)
	p.Verify = func(data json.RawMessage) ([]Plan, error) {
		var acknowledged calendarResponse
		if json.Unmarshal(data, &acknowledged) != nil || !calendarIdentifier(acknowledged.ID) || !calendarETag(acknowledged.ETag) || acknowledged.Status == "cancelled" || op == "calendar.reschedule" && (acknowledged.ID != in.EventID || acknowledged.ETag == in.ETag) {
			return nil, fault("event_acknowledgement_missing")
		}
		read := readPlan(base + "/" + url.PathEscape(acknowledged.ID))
		read.Filter = func(data []byte) (json.RawMessage, error) {
			var event calendarResponse
			if json.Unmarshal(data, &event) != nil || event.ID != acknowledged.ID || event.ETag != acknowledged.ETag || !calendarETag(event.ETag) || event.Status != "confirmed" && event.Status != "tentative" || event.AttendeesOmitted {
				return nil, fault("event_readback_mismatch")
			}
			actualStart, e := time.Parse(time.RFC3339, event.Start.DateTime)
			actualEnd, e2 := time.Parse(time.RFC3339, event.End.DateTime)
			if e != nil || e2 != nil || !actualStart.Equal(start) || !actualEnd.Equal(end) || event.Start.TimeZone != in.Timezone || event.End.TimeZone != in.Timezone {
				return nil, fault("event_readback_mismatch")
			}
			wantSummary := in.Summary
			wantAttendees := expectedAttendees
			if op == "calendar.reschedule" {
				wantSummary = original.Summary
				wantAttendees = []string{}
				for _, a := range original.Attendees {
					wantAttendees = append(wantAttendees, strings.ToLower(a.Email))
				}
				slices.Sort(wantAttendees)
			}
			actualAttendees := []string{}
			for _, a := range event.Attendees {
				actualAttendees = append(actualAttendees, strings.ToLower(a.Email))
			}
			slices.Sort(actualAttendees)
			if event.Summary != wantSummary || !slices.Equal(actualAttendees, wantAttendees) {
				return nil, fault("event_readback_mismatch")
			}
			return providerJSON(map[string]string{"id": event.ID, "etag": event.ETag, "verification": "read_back_verified", "notification_delivery": "unverified"}), nil
		}
		return []Plan{read}, nil
	}
	return p, nil
}
