package openai

import (
	"bytes"
	"errors"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// Responses message snapshots are authoritative, even when a gateway omits or
// changes text deltas. Both transports must reconcile them before ending the
// request so the agent history and the visible stream contain the same reply.
type responsesTextStream struct {
	items   []*responsesTextItem
	emitted strings.Builder
}

type responsesTextItem struct {
	id      string
	index   int
	phase   providers.MessagePhase
	content strings.Builder
}

func (p *responsesTextStream) item(id string, index int) *responsesTextItem {
	var item *responsesTextItem
	for _, candidate := range p.items {
		if id != "" && candidate.id == id {
			item = candidate
			break
		}
	}
	if item == nil && index >= 0 {
		for _, candidate := range p.items {
			if candidate.index == index {
				item = candidate
				break
			}
		}
	}
	if item == nil && len(p.items) > 0 {
		last := p.items[len(p.items)-1]
		// Some compatible endpoints omit identity on deltas. Attach those to
		// the current message, and bind an anonymous first delta when its
		// message identity arrives. Never merge distinct identified messages.
		if (id == "" && index < 0) || (last.id == "" && last.index < 0) {
			item = last
		}
	}
	if item == nil {
		item = &responsesTextItem{index: -1}
		p.items = append(p.items, item)
	}
	if id != "" {
		item.id = id
	}
	if index >= 0 && item.index != index {
		item.index = index
		sort.SliceStable(p.items, func(i, j int) bool {
			if p.items[i].index < 0 {
				return false
			}
			return p.items[j].index < 0 || p.items[i].index < p.items[j].index
		})
	}
	return item
}

func (p *responsesTextStream) setSnapshot(item responsesOutputItem, index int) error {
	text := p.item(item.ID, index)
	if phase := providers.NormalizeMessagePhase(item.Phase); phase != "" {
		text.phase = phase
	}
	// Metadata-only snapshots are used by compatible endpoints. Absence is
	// not an instruction to erase deltas; an explicit empty content array is.
	if len(item.Content) == 0 || bytes.Equal(bytes.TrimSpace(item.Content), []byte("null")) {
		return nil
	}
	parts, err := parseResponsesContentParts(item.Content)
	if err != nil {
		return errors.New("invalid Responses message content")
	}
	text.content.Reset()
	// Match the text delta stream without inventing separators between parts.
	for _, part := range parts {
		text.content.WriteString(part)
	}
	return nil
}

func (p *responsesTextStream) emitSnapshot(emit *providers.StreamEmitter) {
	if len(p.items) == 0 {
		return
	}
	var full strings.Builder
	for _, item := range p.items {
		full.WriteString(item.content.String())
	}
	content := full.String()
	last := p.items[len(p.items)-1]
	event := providers.StreamEvent{Type: providers.EventContentDelta, Phase: last.phase, ProviderItemID: last.id}
	if strings.HasPrefix(content, p.emitted.String()) {
		event.Content = content[p.emitted.Len():]
		p.emitted.WriteString(event.Content)
	} else {
		// ContentReplace replaces the entire request's text, not just this
		// output item. Retain earlier messages when correcting a later one.
		event.Type = providers.EventContentReplace
		event.Content = content
		p.emitted.Reset()
		p.emitted.WriteString(content)
	}
	emit.Send(event)
}

func (p *responsesTextStream) consume(event responsesStreamEvent, emit *providers.StreamEmitter) error {
	switch event.Type {
	case "response.output_text.delta", "response.refusal.delta":
		if event.Delta == "" {
			return nil
		}
		item := p.item(event.ItemID, event.outputIndex())
		item.content.WriteString(event.Delta)
		if item == p.items[len(p.items)-1] {
			// Keep the common delta path linear in response size.
			p.emitted.WriteString(event.Delta)
			emit.Send(providers.StreamEvent{Type: providers.EventContentDelta, Content: event.Delta, Phase: item.phase, ProviderItemID: item.id})
		} else {
			p.emitSnapshot(emit)
		}
	case "response.output_item.added":
		if event.Item.Type == "message" {
			item := p.item(event.Item.ID, event.outputIndex())
			item.phase = providers.NormalizeMessagePhase(event.Item.Phase)
			emit.Send(providers.StreamEvent{Type: providers.EventContentDelta, Phase: item.phase, ProviderItemID: item.id})
		}
	case "response.output_item.done":
		if event.Item.Type == "message" {
			if err := p.setSnapshot(event.Item, event.outputIndex()); err != nil {
				return err
			}
			p.emitSnapshot(emit)
		}
	case "response.completed", "response.done", "response.incomplete":
		if event.Response != nil {
			for index, item := range event.Response.Output {
				if item.Type == "message" {
					if err := p.setSnapshot(item, index); err != nil {
						return err
					}
				}
			}
			p.emitSnapshot(emit)
		}
	}
	return nil
}
