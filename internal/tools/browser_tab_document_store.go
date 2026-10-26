package tools

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// BrowserTabDocumentNamespace stores browser business state independently of
// the desktop host's ephemeral live tabs.
const BrowserTabDocumentNamespace = "wuu.browser-tabs.v1"

type BrowserTabDocumentStore struct {
	documents   persistence.Documents
	scope       string
	legacyPaths []string
}

// NewBrowserTabDocumentStore binds tab ownership to the selected document
// provider. Historical files are imported once, never used as an error fallback.
func NewBrowserTabDocumentStore(documents persistence.Documents, scope string, legacyPaths ...string) *BrowserTabDocumentStore {
	return &BrowserTabDocumentStore{documents: documents, scope: scope, legacyPaths: append([]string(nil), legacyPaths...)}
}

func (s *BrowserTabDocumentStore) access(body func(*persistence.DocumentImage, *[]BrowserTabRecord) error) error {
	return persistence.AccessDocumentImage(context.Background(), s.documents, BrowserTabDocumentNamespace, s.scope, func(image *persistence.DocumentImage) error {
		merged := map[string]BrowserTabRecord{}
		for _, path := range s.legacyPaths {
			if strings.TrimSpace(path) == "" {
				continue
			}
			records, err := NewBrowserTabStore(path).List()
			if err != nil {
				return err
			}
			for _, record := range records {
				previous, ok := merged[record.TabID]
				if !ok || record.UpdatedAt.After(previous.UpdatedAt) {
					merged[record.TabID] = record
				}
			}
		}
		records := make([]BrowserTabRecord, 0, len(merged))
		for _, record := range merged {
			records = append(records, record)
		}
		sort.Slice(records, func(i, j int) bool { return records[i].TabID < records[j].TabID })
		return image.Put("tabs", records)
	}, func(image *persistence.DocumentImage) error {
		var records []BrowserTabRecord
		if err := image.Get("tabs", &records); err != nil {
			return err
		}
		return body(image, &records)
	})
}

func (s *BrowserTabDocumentStore) List() (out []BrowserTabRecord, err error) {
	err = s.access(func(_ *persistence.DocumentImage, records *[]BrowserTabRecord) error {
		out = append([]BrowserTabRecord(nil), (*records)...)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].TabID < out[j].TabID })
	return
}
func (s *BrowserTabDocumentStore) Get(tabID string) (out BrowserTabRecord, found bool, err error) {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return out, false, errors.New("tab_id is required")
	}
	err = s.access(func(_ *persistence.DocumentImage, records *[]BrowserTabRecord) error {
		out = BrowserTabRecord{}
		found = false
		for _, record := range *records {
			if record.TabID == tabID {
				out = record
				found = true
				break
			}
		}
		return nil
	})
	return
}
func (s *BrowserTabDocumentStore) Put(record BrowserTabRecord) error {
	record.TabID = strings.TrimSpace(record.TabID)
	if record.TabID == "" {
		return errors.New("tab_id is required")
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now().UTC()
	}
	return s.access(func(image *persistence.DocumentImage, records *[]BrowserTabRecord) error {
		for i := range *records {
			if (*records)[i].TabID == record.TabID {
				(*records)[i] = record
				return image.Put("tabs", *records)
			}
		}
		*records = append(*records, record)
		return image.Put("tabs", *records)
	})
}
func (s *BrowserTabDocumentStore) Delete(tabID string) error {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return errors.New("tab_id is required")
	}
	return s.access(func(image *persistence.DocumentImage, records *[]BrowserTabRecord) error {
		for i, record := range *records {
			if record.TabID == tabID {
				*records = append((*records)[:i], (*records)[i+1:]...)
				return image.Put("tabs", *records)
			}
		}
		return nil
	})
}
