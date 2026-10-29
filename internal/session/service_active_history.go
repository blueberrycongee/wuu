package session

// Additive selected-provider operation; the generated operation inventory stays
// unchanged. A provider must explicitly implement the active projection contract.
var activeHistoryDomainMethods = []string{"ReadActiveHistoryQuery"}

// ReadActiveHistoryQuery reads a bounded physical address range excluding current
// history retractions. Snapshot/cursor bounds never revive an edited source.
// Providers without this operation fail explicitly; no physical-read fallback is safe.
func (s *Store) ReadActiveHistoryQuery(query HistoryReadQuery) (HistoryPage, error) {
	var result HistoryPage
	err := s.invoke("ReadActiveHistoryQuery", []any{query}, []any{&result})
	return result, err
}
