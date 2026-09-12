package channels

import (
	"context"
	"testing"
)

func TestNamedAgentAccessoryCollectionPersists(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	created := createTestAgent(t, service, "Mira")
	for _, accessory := range []string{"beanie", "hard-hat", "headset", "bandana", "leaf"} {
		key := "mascot-v1:cloud:" + accessory + ":202"
		_, err := service.UpdateNamedAgent(ctx, UpdateNamedAgentParams{ID: created.Agent.ID, Name: "Mira", AvatarKey: key})
		if err != nil {
			t.Fatal(err)
		}
		stored, err := service.GetNamedAgent(ctx, created.Agent.ID)
		if err != nil || stored.AvatarKey != key {
			t.Fatalf("saved accessory %s: got %q, %v", accessory, stored.AvatarKey, err)
		}
	}
	// A saved identity containing removed artwork can still be edited. Its
	// shape and hue survive, without keeping the old catalogue in the product.
	_, err := service.UpdateNamedAgent(ctx, UpdateNamedAgentParams{ID: created.Agent.ID, Name: "Mira", AvatarKey: "mascot-v1:cloud:crown:202"})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := service.GetNamedAgent(ctx, created.Agent.ID)
	if err != nil || stored.AvatarKey != "mascot-v1:cloud:none:202" {
		t.Fatalf("removed accessory reset identity: %#v, %v", stored, err)
	}
}
