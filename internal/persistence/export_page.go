package persistence

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
	"strings"
)

const (
	DefaultExportTargetBytes = 32 << 20
	DefaultExportMaxItems    = 256
	ExportInputSchema        = "state.documents.export.input.v2"
	ExportOutputSchema       = "state.documents.export.output.v2"
)

// ExportCursor is exclusive in the selected section's order. Documents use the
// namespace/key tuple; receipts use their numeric revision. The forms cannot mix.
type ExportCursor struct {
	Namespace string    `json:"namespace,omitempty"`
	Key       string    `json:"key,omitempty"`
	Revision  *Revision `json:"revision,string,omitempty"`
}

// ExportQuery pins a traversal to one revision and physical generation. A first
// documents page may omit both pins. TargetBytes counts the entire encoded page;
// only a page containing exactly one record may exceed it. MaxItems is mandatory.
type ExportQuery struct {
	Section     string        `json:"section"`
	AtRevision  *Revision     `json:"at_revision,string,omitempty"`
	Generation  string        `json:"generation,omitempty"`
	After       *ExportCursor `json:"after,omitempty"`
	TargetBytes int           `json:"target_bytes"`
	MaxItems    int           `json:"max_items"`
}

// ExportPage carries actual records, never a provider-supplied checkpoint. A
// terminal page exhausts its section; an empty nonterminal page is invalid.
type ExportPage struct {
	Format     string         `json:"format"`
	Revision   Revision       `json:"revision,string"`
	Generation string         `json:"generation"`
	Section    string         `json:"section"`
	Documents  []Document     `json:"documents"`
	Receipts   []CommitRecord `json:"receipts"`
	Done       bool           `json:"done"`
}

// DecodeExportRequest preserves the legacy empty request and validates explicit
// page requests before any storage access. A null page is not a legacy request.
func DecodeExportRequest(raw json.RawMessage) (*ExportQuery, error) {
	var request struct {
		Page json.RawMessage `json:"page"`
	}
	if err := decode(raw, &request); err != nil {
		return nil, err
	}
	if request.Page == nil {
		return nil, nil
	}
	if string(request.Page) == "null" {
		return nil, fmt.Errorf("%w: page must be an object", ErrInvalid)
	}
	var query ExportQuery
	if err := decode(request.Page, &query); err != nil {
		return nil, err
	}
	if err := validateExportQuery(query); err != nil {
		return nil, err
	}
	return &query, nil
}

func validateExportQuery(q ExportQuery) error {
	if q.Section != "documents" && q.Section != "receipts" {
		return fmt.Errorf("%w: export section must be documents or receipts", ErrInvalid)
	}
	if q.TargetBytes <= 0 || q.TargetBytes > DefaultExportTargetBytes || q.MaxItems <= 0 || q.MaxItems > MaxReadLimit {
		return fmt.Errorf("%w: export requires a positive target_bytes <= %d and max_items <= %d", ErrInvalid, DefaultExportTargetBytes, MaxReadLimit)
	}
	if (q.AtRevision == nil) != (q.Generation == "") {
		return fmt.Errorf("%w: export revision and generation must be pinned together", ErrInvalid)
	}
	if (q.Section == "receipts" || q.After != nil) && q.AtRevision == nil {
		return fmt.Errorf("%w: export continuation requires revision and generation", ErrInvalid)
	}
	if q.After != nil {
		if q.Section == "documents" {
			if strings.TrimSpace(q.After.Namespace) == "" || q.After.Key == "" || q.After.Revision != nil {
				return fmt.Errorf("%w: document cursor requires only namespace and key", ErrInvalid)
			}
		} else if q.After.Revision == nil || q.After.Namespace != "" || q.After.Key != "" || *q.After.Revision > *q.AtRevision {
			return fmt.Errorf("%w: receipt cursor requires only a revision within the snapshot", ErrInvalid)
		}
	}
	return nil
}

func exportJSONSize(value any) (int, error) {
	var size wireByteCount
	err := jsonv2.MarshalWrite(&size, value, json.DefaultOptionsV1())
	return int(size), err
}

type exportPageBuilder struct {
	page        ExportPage
	query       ExportQuery
	size, items int
}

func newExportPage(q ExportQuery, revision Revision, generation string) (*exportPageBuilder, error) {
	if q.Generation != "" && q.Generation != generation {
		return nil, ErrBindingChanged
	}
	if q.AtRevision != nil && *q.AtRevision != revision {
		return nil, ErrConflict
	}
	page := ExportPage{Format: "page.v1", Revision: revision, Generation: generation, Section: q.Section, Documents: []Document{}, Receipts: []CommitRecord{}}
	size, err := exportJSONSize(page)
	if err != nil {
		return nil, err
	}
	if size > q.TargetBytes {
		return nil, fmt.Errorf("%w: export target_bytes cannot fit the page envelope", ErrInvalid)
	}
	return &exportPageBuilder{page: page, query: q, size: size}, nil
}
func (b *exportPageBuilder) reserve(record any) (bool, error) {
	if b.items == b.query.MaxItems {
		return false, nil
	}
	size, err := exportJSONSize(record)
	if err != nil {
		return false, err
	}
	if b.items > 0 {
		size++
	}
	if b.items > 0 && b.size+size > b.query.TargetBytes {
		return false, nil
	}
	b.size += size
	b.items++
	return true, nil
}
func (b *exportPageBuilder) addDocument(d Document) (bool, error) {
	fits, err := b.reserve(d)
	if fits {
		b.page.Documents = append(b.page.Documents, d)
	}
	return fits, err
}
func (b *exportPageBuilder) addReceipt(r CommitRecord) (bool, error) {
	fits, err := b.reserve(r)
	if fits {
		b.page.Receipts = append(b.page.Receipts, r)
	}
	return fits, err
}

func (s *Store) ExportPage(ctx context.Context, q ExportQuery) (ExportPage, error) {
	if err := validateExportQuery(q); err != nil {
		return ExportPage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := s.acquire(ctx)
	if err != nil {
		return ExportPage{}, err
	}
	defer lock.Release()
	return s.backend.exportPage(ctx, q, s.binding.Generation)
}

func validateExportPage(page ExportPage, q ExportQuery) error {
	if page.Format != "page.v1" || page.Generation == "" || page.Section != q.Section || page.Documents == nil || page.Receipts == nil {
		return fmt.Errorf("%w: invalid export page envelope", ErrCorrupt)
	}
	if q.Generation != "" && page.Generation != q.Generation {
		return ErrBindingChanged
	}
	if q.AtRevision != nil && page.Revision != *q.AtRevision {
		return ErrConflict
	}
	count := len(page.Documents) + len(page.Receipts)
	if count > q.MaxItems || count == 0 && !page.Done {
		return fmt.Errorf("%w: export page exceeds bounds or makes no progress", ErrCorrupt)
	}
	if q.Section == "documents" && len(page.Receipts) != 0 || q.Section == "receipts" && len(page.Documents) != 0 {
		return fmt.Errorf("%w: export page mixes sections", ErrCorrupt)
	}
	return nil
}

// MarshalExportPage retains one result buffer, including for the single-record
// exception. Its encoding is the same as the checkpoint's JSON v1 encoding.
func MarshalExportPage(page ExportPage) (json.RawMessage, error) {
	return marshalWire(func(w io.Writer) error { return jsonv2.MarshalWrite(w, page, json.DefaultOptionsV1()) })
}
