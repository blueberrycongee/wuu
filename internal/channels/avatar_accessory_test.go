package channels

import (
	"context"
	"testing"
)

func TestNamedAgentAccessoryCollectionPersists(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	created := createTestAgent(t, service, "Mira")
	for _, accessory := range []string{"beanie", "hard-hat", "headset", "leaf"} {
		key := "mascot-v1:capsule:" + accessory + ":202"
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
	_, err := service.UpdateNamedAgent(ctx, UpdateNamedAgentParams{ID: created.Agent.ID, Name: "Mira", AvatarKey: "mascot-v1:cloud:bandana:202"})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := service.GetNamedAgent(ctx, created.Agent.ID)
	if err != nil || stored.AvatarKey != "mascot-v1:capsule:none:202" {
		t.Fatalf("removed accessory reset identity: %#v, %v", stored, err)
	}
}

func TestNamedAgentShapeCollectionPersists(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	created := createTestAgent(t, service, "Mira")
	for _, shape := range []string{"round", "rounded-square", "capsule", "triangle", "diamond", "organic", "boxy", "nub", "cloud", "sun"} {
		key := "mascot-v1:" + shape + ":headset:202"
		_, err := service.UpdateNamedAgent(ctx, UpdateNamedAgentParams{ID: created.Agent.ID, Name: "Mira", AvatarKey: key})
		if err != nil {
			t.Fatal(err)
		}
		stored, err := service.GetNamedAgent(ctx, created.Agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		valid := false
		for _, current := range []string{"round", "rounded-square", "capsule", "triangle", "diamond"} {
			valid = valid || stored.AvatarKey == "mascot-v1:"+current+":headset:202"
		}
		if !valid {
			t.Fatalf("shape %s lost its color or headwear: %q", shape, stored.AvatarKey)
		}
	}
}
