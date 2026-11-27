package session

import (
	"errors"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

var placementDomainMethods = []string{"PromoteSideChat", "DeleteWithSideChats"}

func (s *Store) DeleteWithSideChats(id string, children []string, onlyIfArchived bool) ([]Session, error) {
	var result []Session
	err := s.invoke("DeleteWithSideChats", []any{id, children, onlyIfArchived}, []any{&result})
	return result, err
}

func (s *Store) PromoteSideChat(id string) (Session, error) {
	var result Session
	err := s.invoke("PromoteSideChat", []any{id}, []any{&result})
	return result, err
}

func (s *documentStore) PromoteSideChat(id string) (Session, error) {
	var result Session
	err := s.transaction(func(tx *persistence.Tx) error {
		thread, err := requireSessionDocument(tx, id)
		if err != nil {
			return err
		}
		if thread.Placement != PlacementSide {
			return errors.New("thread is not an attached side chat")
		}
		thread.Placement, thread.ParentID = "", ""
		if err := putSessionDocument(tx, thread); err != nil {
			return err
		}
		result = thread
		return nil
	})
	return result, err
}
