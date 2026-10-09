package runtime

import (
	"context"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type cloneGuardClient struct{}

func (cloneGuardClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, nil
}

type cloneGuardStreamClient struct{ cloneGuardClient }

func (cloneGuardStreamClient) StreamChat(context.Context, providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	return nil, nil
}

// cloneForThreadModelReplacedFields lists exported Session fields that
// cloneForThreadModel intentionally does not carry over verbatim (the caller
// replaces them right after cloning). Every other exported field must survive
// the clone, so adding a field to Session without updating cloneForThreadModel
// fails TestCloneForThreadModelCopiesEveryExportedField.
var cloneForThreadModelReplacedFields = map[string]bool{}

func fillCloneGuardValue(t *testing.T, path string, v reflect.Value, depth int) bool {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
	case reflect.Struct:
		filled := false
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if !field.CanSet() {
				continue
			}
			if fillCloneGuardValue(t, path+"."+v.Type().Field(i).Name, field, depth+1) {
				filled = true
			}
		}
		return filled
	case reflect.Interface:
		switch v.Type() {
		case reflect.TypeOf((*providers.StreamClient)(nil)).Elem():
			v.Set(reflect.ValueOf(cloneGuardStreamClient{}))
		case reflect.TypeOf((*providers.Client)(nil)).Elem():
			v.Set(reflect.ValueOf(cloneGuardClient{}))
		default:
			if depth == 0 {
				t.Fatalf("no clone-guard filler for interface field %s (%s); add one so the field stays covered", path, v.Type())
			}
			return false
		}
	default:
		if depth == 0 {
			t.Fatalf("no clone-guard filler for field %s (kind %s); add one so the field stays covered", path, v.Kind())
		}
		return false
	}
	return true
}

// TestCloneForThreadModelCopiesEveryExportedField populates every exported
// Session field with a non-zero value via reflection, clones, and fails on any
// exported field the clone left at its zero value. This is the tripwire for
// "added a Session field but forgot cloneForThreadModel": a missed copy would
// silently strip runtime state from every per-thread shadow session.
func TestCloneForThreadModelCopiesEveryExportedField(t *testing.T) {
	original := &Session{}
	value := reflect.ValueOf(original).Elem()
	kind := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := kind.Field(i)
		if !field.IsExported() {
			continue
		}
		fillCloneGuardValue(t, field.Name, value.Field(i), 0)
		if value.Field(i).IsZero() {
			t.Fatalf("clone-guard filler left Session.%s zero; extend fillCloneGuardValue", field.Name)
		}
	}

	var err error
	original.Toolkit, err = tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clone, err := original.cloneForThreadModel()
	if err != nil {
		t.Fatal(err)
	}
	cloneValue := reflect.ValueOf(clone).Elem()
	for i := 0; i < cloneValue.NumField(); i++ {
		field := kind.Field(i)
		if !field.IsExported() || cloneForThreadModelReplacedFields[field.Name] {
			continue
		}
		if cloneValue.Field(i).IsZero() {
			t.Errorf("cloneForThreadModel does not copy Session.%s; copy it or allowlist it as intentionally replaced", field.Name)
		}
	}
}
