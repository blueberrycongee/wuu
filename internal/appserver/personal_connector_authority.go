package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/session"
	"strconv"
)

// connectorAuthority fingerprints host-owned task state, not tool arguments.
// Confirmation is invalid after task cancellation, a new requirement revision,
// session archival, plugin disablement, or a newer human turn in the main chat.
func (p *personalActions) connectorAuthority(ctx context.Context, actor personalconnectors.Actor) (string, error) {
	binding := p.server.rt.UserBinding()
	if actor.ProductDomain != "personal" || actor.UserSpaceID != binding.UserSpaceID || actor.SessionID == "" || p.server.rt.SafeMode {
		return "", errors.New("Personal connector authority is unavailable")
	}
	snapshot := p.server.rt.ExtensionSnapshot()
	subject := ""
	fingerprint := ""
	for _, item := range snapshot.ActivePlugins {
		if item.ID == "personal" {
			subject = item.SubjectID
			fingerprint = item.Fingerprint
			break
		}
	}
	if subject == "" || snapshot.PluginHost == nil {
		return "", errors.New("Personal plugin is unavailable")
	}
	metadata, found, err := session.Find(p.server.rt.SessionDir, actor.SessionID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("Personal session is unavailable")
	}
	taskID := ""
	if metadata.ParentID == "" {
		head, err := session.ReadHistoryPage(ctx, p.server.rt.SessionDir, actor.SessionID, 1, 1)
		if err != nil {
			return "", err
		}
		start := max(1, head.HeadSeq-255)
		page, err := session.ReadHistoryPage(ctx, p.server.rt.SessionDir, actor.SessionID, start, 256)
		if err != nil {
			return "", err
		}
		seq := 0
		for _, record := range page.Records {
			if record.Role == "user" && record.Origin == "user" && !record.Hidden && !record.ReadOnly {
				seq = record.Seq
			}
		}
		if seq == 0 {
			return "", errors.New("human source turn is unavailable")
		}
		taskID = "conversation:" + actor.SessionID + ":" + strconv.Itoa(seq)
	}
	var digest string
	err = pluginsettings.InspectState(p.stateRoot(), p.server.rt.RootDir, subject, pluginsettings.ScopeUser, func(doc pluginsettings.StateDocument) error {
		authority, err := p.authority(ctx, actor.SessionID, taskID, doc)
		if err != nil {
			return err
		}
		if !authority.Active || authority.BoundaryReason != "" {
			return errors.New("Personal task authority changed")
		}
		raw, err := json.Marshal(struct {
			Scope  any
			Plugin string
		}{authority.Scope, fingerprint})
		if err != nil {
			return err
		}
		digest = personalHash(raw)
		return nil
	})
	return digest, err
}
