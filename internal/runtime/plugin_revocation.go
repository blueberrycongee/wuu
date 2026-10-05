package runtime

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Revocation phases, in the order a generation is retired.
const (
	RevocationPhaseCancelExecutions = "cancel-executions"
	RevocationPhaseRevokeServices   = "revoke-services"
	RevocationPhaseShutdown         = "shutdown"
	RevocationPhaseCleanup          = "cleanup"
)

// Revocation outcomes.
const (
	RevocationOutcomeRevoked = "revoked"
	RevocationOutcomeFailed  = "failed"
)

// ResourceRevocation records how one generation-owned resource was retired at
// one phase, attributed to the owning plugin when the resource belongs to a
// single plugin.
type ResourceRevocation struct {
	GenerationID string `json:"generation_id"`
	PluginID     string `json:"plugin_id,omitempty"`
	Resource     string `json:"resource"`
	Phase        string `json:"phase"`
	Outcome      string `json:"outcome"`
	Detail       string `json:"detail,omitempty"`
}

// GenerationRevocationReport is the structured record of retiring one plugin
// generation: every owned resource, every phase, and whether revocation
// succeeded. Reports persist so cleanup failures of retired generations stay
// visible after the generation itself is gone.
type GenerationRevocationReport struct {
	GenerationID string               `json:"generation_id"`
	RetiredAt    time.Time            `json:"retired_at"`
	Resources    []ResourceRevocation `json:"resources"`
}

// Failed reports whether any resource could not be revoked.
func (r *GenerationRevocationReport) Failed() bool {
	if r == nil {
		return false
	}
	for _, resource := range r.Resources {
		if resource.Outcome == RevocationOutcomeFailed {
			return true
		}
	}
	return false
}

func (r *GenerationRevocationReport) record(pluginID, resource, phase string, err error) {
	if r == nil {
		return
	}
	entry := ResourceRevocation{
		GenerationID: r.GenerationID,
		PluginID:     pluginID,
		Resource:     resource,
		Phase:        phase,
		Outcome:      RevocationOutcomeRevoked,
	}
	if err != nil {
		entry.Outcome = RevocationOutcomeFailed
		entry.Detail = err.Error()
	}
	r.Resources = append(r.Resources, entry)
}

func (r *GenerationRevocationReport) recordDetail(pluginID, resource, phase, detail string, err error) {
	r.record(pluginID, resource, phase, err)
	if r == nil || len(r.Resources) == 0 || detail == "" {
		return
	}
	r.Resources[len(r.Resources)-1].Detail = detail
	if err != nil {
		r.Resources[len(r.Resources)-1].Detail = detail + ": " + err.Error()
	}
}

var pluginGenerationSequence atomic.Uint64

// newPluginGenerationID correlates a generation with the durable mutation
// epoch it was built from while staying unique across processes that share
// one Wuu home.
func newPluginGenerationID(wuuHome string) string {
	epoch, err := session.ReadPluginGenerationEpoch(wuuHome)
	if err != nil {
		epoch = 0
	}
	return fmt.Sprintf("gen-%d-%d-%d", epoch, os.Getpid(), pluginGenerationSequence.Add(1))
}

const pluginGenerationRevocationFile = "plugin-generation-revocations.jsonl"

// appendGenerationRevocation persists one retired-generation report. Reports
// are append-only so a failed revocation cannot be rewritten by a later one.
func appendGenerationRevocation(wuuHome string, report *GenerationRevocationReport) error {
	wuuHome = strings.TrimSpace(wuuHome)
	if wuuHome == "" || report == nil {
		return nil
	}
	if err := os.MkdirAll(wuuHome, securefs.DirMode); err != nil {
		return fmt.Errorf("create Wuu home: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(wuuHome, pluginGenerationRevocationFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, securefs.FileMode)
	if err != nil {
		return fmt.Errorf("open plugin generation revocations: %w", err)
	}
	defer file.Close()
	line, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode plugin generation revocation: %w", err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write plugin generation revocation: %w", err)
	}
	return nil
}

// readGenerationRevocations returns the newest reports first, capped at the
// requested limit; zero or negative limit returns everything retained.
func readGenerationRevocations(wuuHome string, limit int) ([]GenerationRevocationReport, error) {
	wuuHome = strings.TrimSpace(wuuHome)
	if wuuHome == "" {
		return nil, errors.New("Wuu home is required")
	}
	file, err := os.Open(filepath.Join(wuuHome, pluginGenerationRevocationFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open plugin generation revocations: %w", err)
	}
	defer file.Close()
	var reports []GenerationRevocationReport
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var report GenerationRevocationReport
		if err := json.Unmarshal([]byte(line), &report); err != nil {
			continue
		}
		reports = append(reports, report)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read plugin generation revocations: %w", err)
	}
	for left, right := 0, len(reports)-1; left < right; left, right = left+1, right-1 {
		reports[left], reports[right] = reports[right], reports[left]
	}
	if limit > 0 && len(reports) > limit {
		reports = reports[:limit]
	}
	return reports, nil
}
