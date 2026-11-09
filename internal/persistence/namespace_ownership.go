package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/persistence/inspectionjournal"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// NamespaceOwnershipNamespace is reserved provider metadata. Only claim-namespaces
// or an explicitly admitted migration may create it; ordinary commits cannot.
const NamespaceOwnershipNamespace = "wuu.state.namespace_owners.v1"
const namespaceOwnershipNamespace = NamespaceOwnershipNamespace

var ErrNamespaceOwnership = errors.New("selected namespace belongs to another consumer")
var ErrNamespaceOwnershipUnsupported = errors.New("selected provider does not support owned namespaces")

type NamespaceOwner interface {
	ClaimNamespaces(context.Context, []string) error
}

func ClaimNamespaces(ctx context.Context, documents Documents, namespaces []string) error {
	owner, ok := documents.(NamespaceOwner)
	if !ok {
		return ErrNamespaceOwnershipUnsupported
	}
	return owner.ClaimNamespaces(ctx, namespaces)
}
func (c *Client) ClaimNamespaces(ctx context.Context, namespaces []string) error {
	var result struct{}
	err := c.invoke(ctx, "claim-namespaces", struct {
		Namespaces []string `json:"namespaces"`
	}{namespaces}, &result)
	var host *pluginhost.HostServiceError
	if errors.As(err, &host) && host.Code == "method_not_found" {
		return errors.Join(ErrNamespaceOwnershipUnsupported, err)
	}
	return err
}

type namespaceIdentity struct {
	caller    string
	steward   bool
	migration bool
}
type namespaceIdentityKey struct{}

func withNamespaceCaller(ctx context.Context, caller string) context.Context {
	return context.WithValue(ctx, namespaceIdentityKey{}, namespaceIdentity{caller: caller})
}
func namespaceIdentityFrom(ctx context.Context) namespaceIdentity {
	identity, ok := ctx.Value(namespaceIdentityKey{}).(namespaceIdentity)
	if !ok {
		return namespaceIdentity{caller: "local"}
	}
	return identity
}

type namespaceOwnership struct {
	Version   int    `json:"version"`
	Namespace string `json:"namespace"`
	Owner     string `json:"owner"`
}

func decodeNamespaceOwner(document Document) (namespaceOwnership, error) {
	var owner namespaceOwnership
	if decode(document.Value, &owner) != nil || owner.Version != 1 || owner.Namespace != document.Key || owner.Namespace == namespaceOwnershipNamespace || strings.TrimSpace(owner.Namespace) != owner.Namespace || owner.Namespace == "" || len(owner.Namespace) > 1024 || strings.TrimSpace(owner.Owner) != owner.Owner || owner.Owner == "" {
		return owner, fmt.Errorf("%w: invalid namespace ownership metadata", ErrCorrupt)
	}
	return owner, nil
}
func (s *Store) namespaceOwners(ctx context.Context) (map[string]string, error) {
	owners := make(map[string]string)
	query := Query{Namespace: namespaceOwnershipNamespace, Limit: 256}
	for {
		view, err := s.backend.read(ctx, query)
		if err != nil {
			return nil, err
		}
		for _, doc := range view.Documents {
			owner, err := decodeNamespaceOwner(doc)
			if err != nil {
				return nil, err
			}
			owners[owner.Namespace] = owner.Owner
		}
		if view.NextCursor == "" {
			return owners, nil
		}
		query.Cursor, query.AtRevision = view.NextCursor, &view.Revision
	}
}

// The provider's transaction lock must cover this check and its subsequent read
// or mutation. Ownership metadata is part of the same portable store snapshot.
func (s *Store) checkNamespaceAccess(ctx context.Context, namespaces []string) error {
	identity := namespaceIdentityFrom(ctx)
	owners, err := s.namespaceOwners(ctx)
	if err != nil {
		return err
	}
	if identity.steward {
		return nil
	}
	if namespaces == nil {
		for _, owner := range owners {
			if owner != identity.caller {
				return ErrNamespaceOwnership
			}
		}
		return nil
	}
	for _, namespace := range namespaces {
		if namespace == namespaceOwnershipNamespace {
			return ErrNamespaceOwnership
		}
		if owner, found := owners[namespace]; found && owner != identity.caller {
			return ErrNamespaceOwnership
		}
	}
	return nil
}

// PrepareNamespaceClaim calculates the exact atomic transaction used by the native
// claim protocol. A configured steward uses it to journal before/after checkpoints
// before mutation. Reads are pinned to one revision; conflict is never adopted.
// A result with no writes means all requested claims already belong to owner.
func PrepareNamespaceClaim(ctx context.Context, documents Documents, owner string, namespaces []string) (Transaction, error) {
	if strings.TrimSpace(owner) != owner || owner == "" || len(namespaces) == 0 || len(namespaces) > 128 {
		return Transaction{}, ErrInvalid
	}
	namespaces = slices.Clone(namespaces)
	slices.Sort(namespaces)
	namespaces = slices.Compact(namespaces)
	for _, namespace := range namespaces {
		if strings.TrimSpace(namespace) != namespace || namespace == "" || len(namespace) > 1024 || namespace == NamespaceOwnershipNamespace {
			return Transaction{}, ErrInvalid
		}
	}
	view, err := documents.Read(ctx, Query{Namespace: NamespaceOwnershipNamespace, Keys: namespaces})
	if err != nil {
		return Transaction{}, err
	}
	owners := make(map[string]string, len(view.Documents))
	for _, doc := range view.Documents {
		claim, err := decodeNamespaceOwner(doc)
		if err != nil {
			return Transaction{}, err
		}
		owners[claim.Namespace] = claim.Owner
	}
	tx := Transaction{ExpectedRevision: view.Revision}
	for _, namespace := range namespaces {
		if prior, found := owners[namespace]; found {
			if prior != owner {
				return Transaction{}, ErrNamespaceOwnership
			}
			continue
		}
		existing, err := documents.Read(ctx, Query{Namespace: namespace, Limit: 1, AtRevision: &view.Revision})
		if err != nil {
			return Transaction{}, err
		}
		if len(existing.Documents) > 0 {
			return Transaction{}, fmt.Errorf("%w: namespace %s has unowned existing data", ErrMigrationRequired, namespace)
		}
		raw, _ := json.Marshal(namespaceOwnership{1, namespace, owner})
		tx.Writes = append(tx.Writes, Write{Namespace: NamespaceOwnershipNamespace, Key: namespace, Value: raw})
	}
	if len(tx.Writes) == 0 {
		return tx, nil
	}
	// Intent includes the selected revision and canonical ordered writes. The ID
	// must be identical in host prediction and provider commit, including receipts.
	intent, _ := json.Marshal(tx)
	digest := sha256.Sum256(append([]byte("wuu.namespace-claim.v1\x00"), intent...))
	tx.ID = "namespace-claim." + hex.EncodeToString(digest[:])
	return tx, nil
}

type namespaceClaimReader struct{ backend }

func (r namespaceClaimReader) Read(ctx context.Context, q Query) (View, error) {
	return r.backend.read(ctx, q)
}
func (r namespaceClaimReader) Commit(context.Context, Transaction) (Receipt, error) {
	return Receipt{}, ErrInvalid
}

func (s *Store) ClaimNamespaces(ctx context.Context, namespaces []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer lock.Release()
	request, err := PrepareNamespaceClaim(ctx, namespaceClaimReader{s.backend}, namespaceIdentityFrom(ctx).caller, namespaces)
	if err != nil {
		return err
	}
	if len(request.Writes) == 0 {
		return nil
	}
	if err := inspectionjournal.Check(s.dir, namespaces); err != nil {
		return err
	}
	digest, err := transactionDigest(request)
	if err != nil {
		return err
	}
	_, err = s.backend.commit(ctx, request, digest)
	return err
}
func validateSnapshotOwners(ctx context.Context, snapshot Snapshot) error {
	identity := namespaceIdentityFrom(ctx)
	for _, document := range snapshot.Documents {
		if document.Namespace != namespaceOwnershipNamespace {
			continue
		}
		owner, err := decodeNamespaceOwner(document)
		if err != nil {
			return err
		}
		if !identity.steward && owner.Owner != identity.caller {
			return ErrNamespaceOwnership
		}
	}
	return nil
}
