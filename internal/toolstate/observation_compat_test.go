package toolstate

import (
	"encoding/json"
	"testing"
)

func TestObservationOwnerWireCompatibility(t *testing.T) {
	for _, input := range []Observation{
		{WorkerInstance: "host-read-valid-remote", OperationID: "old"},
		{OwnerKind: "session", WorkerInstance: "host-read-local", OperationID: "new"},
	} {
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Observation
		if err := json.Unmarshal(data, &decoded); err != nil || decoded != input {
			t.Fatalf("roundtrip %s: %+v %v", data, decoded, err)
		}
		var old struct{ WorkerInstance string }
		err = json.Unmarshal(data, &old)
		if (err != nil) != (input.OwnerKind == "session") {
			t.Fatalf("old decoder %s: %v", data, err)
		}
	}
	for _, data := range []string{
		`{"OwnerKind":"session","WorkerInstance":"host-read-a"}`,
		`{"WorkerInstance":{"kind":"session","instance":"host-read-a"}}`,
		`{"OwnerKind":"session","WorkerInstance":{"kind":"remote","instance":"a"}}`,
		`{"OwnerKind":"unknown","WorkerInstance":"a"}`,
	} {
		var o Observation
		if err := json.Unmarshal([]byte(data), &o); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}
