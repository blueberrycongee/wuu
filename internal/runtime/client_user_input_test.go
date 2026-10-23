package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// User provenance comes from an explicit authenticated client submission, not
// from a plugin's claim or the model's tool arguments.
func TestClientUserInputProofBindsExactPluginPromptAndTarget(t *testing.T) {
	if err := AuthorizeClientUserInput(context.Background(), "personal", "request", "main", "hello"); err == nil {
		t.Fatal("background plugin forged user input")
	}
	ctx := WithClientUserInput(context.Background(), "personal", ClientUserInput{RequestID: "request", Prompt: "hello"})
	for _, tc := range [][4]string{{"other", "request", "main", "hello"}, {"personal", "other", "main", "hello"}, {"personal", "request", "main", "changed"}} {
		if err := AuthorizeClientUserInput(ctx, tc[0], tc[1], tc[2], tc[3]); err == nil {
			t.Fatalf("accepted changed user input: %v", tc)
		}
	}
	if err := AuthorizeClientUserInput(ctx, "personal", "request", "main", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeClientUserInput(ctx, "personal", "request", "main", "hello"); err != nil {
		t.Fatal("same-target retry rejected", err)
	}
	if err := AuthorizeClientUserInput(ctx, "personal", "request", "other", "hello"); err == nil {
		t.Fatal("one submission redirected to a second target")
	}
}

func TestClientUserInputProofCannotReplayAsAnotherSendOrAlterWhitespace(t *testing.T) {
	ctx := WithClientUserInput(context.Background(), "personal", ClientUserInput{RequestID: "client", Prompt: " hello "})
	if err := AuthorizeClientUserInput(ctx, "personal", "client", "main", "hello", "send-one"); err == nil {
		t.Fatal("trimmed client payload was accepted as exact")
	}
	if err := AuthorizeClientUserInput(ctx, "personal", "client", "main", " hello ", "send-one"); err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeClientUserInput(ctx, "personal", "client", "main", " hello ", "send-two"); err == nil {
		t.Fatal("same admission minted a second send")
	}
}

func TestClientUserInputProofDoesNotSurviveDetachedBackgroundContext(t *testing.T) {
	admission, cancel := context.WithCancel(context.Background())
	admitted := WithClientUserInput(admission, "personal", ClientUserInput{RequestID: "request", Prompt: "hello"})
	detached := context.WithoutCancel(admitted)
	cancel()
	if err := AuthorizeClientUserInput(detached, "personal", "request", "main", "hello"); err == nil {
		t.Fatal("detached background retained expired user authority")
	}
}

// Attachment bytes are admitted only by the client envelope. A plugin cannot
// swap the bytes, add a file, omit a file, or reuse a retired admission.
func TestClientUserInputAttachmentsRequireExactAdmittedReferences(t *testing.T) {
	input := ClientUserInput{RequestID: "r", Prompt: "read this", Attachments: []ClientInputAttachment{{SHA256: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", Filename: "note.txt", MediaType: "text/plain", Data: "aGVsbG8="}}}
	if err := ValidateClientUserInput(input); err != nil {
		t.Fatal(err)
	}
	admission, cancel := context.WithCancel(context.Background())
	ctx := WithClientUserInput(admission, "personal", input)
	ref := pluginhost.SessionInputAttachment{SHA256: input.Attachments[0].SHA256, Filename: "note.txt", MediaType: "text/plain"}
	if _, err := ClientUserInputAttachments(ctx, "personal", "r", nil); err == nil {
		t.Fatal("plugin omitted admitted file")
	}
	wrong := ref
	wrong.Filename = "other.txt"
	if _, err := ClientUserInputAttachments(ctx, "personal", "r", []pluginhost.SessionInputAttachment{wrong}); err == nil {
		t.Fatal("plugin renamed admitted file")
	}
	files, err := ClientUserInputAttachments(ctx, "personal", "r", []pluginhost.SessionInputAttachment{ref})
	if err != nil || len(files) != 1 || files[0].Data != "aGVsbG8=" {
		t.Fatalf("files: %+v %v", files, err)
	}
	input.Attachments[0].Data = "Y2hhbmdlZA=="
	if err := ValidateClientUserInput(input); err == nil {
		t.Fatal("digest did not bind bytes")
	}
	files, err = ClientUserInputAttachments(ctx, "personal", "r", []pluginhost.SessionInputAttachment{ref})
	if err != nil || files[0].Data != "aGVsbG8=" {
		t.Fatal("caller mutated admitted snapshot", err)
	}
	cancel()
	if _, err := ClientUserInputAttachments(context.WithoutCancel(ctx), "personal", "r", []pluginhost.SessionInputAttachment{ref}); err == nil {
		t.Fatal("expired attachment authority survived")
	}
}

func TestClientUserInputRelationCannotBeChangedOrOmitted(t *testing.T) {
	relation := &providers.MessageRelation{ReplyTo: &providers.MessageRef{SessionID: "main", Seq: 3}, Metadata: map[string]json.RawMessage{"task_links": json.RawMessage(`[{"task_id":"one","requirements_revision":2}]`)}}
	ctx := WithClientUserInput(context.Background(), "personal", ClientUserInput{RequestID: "r", Prompt: " exact ", Relation: relation})
	relation.ReplyTo.Seq = 99
	original := &providers.MessageRelation{ReplyTo: &providers.MessageRef{SessionID: "main", Seq: 3}, Metadata: map[string]json.RawMessage{"task_links": json.RawMessage(`[{"task_id":"one","requirements_revision":2}]`)}}
	if err := AuthorizeClientUserInputRelation(ctx, "personal", "r", original); err != nil {
		t.Fatal(err)
	}
	for _, altered := range []*providers.MessageRelation{nil, relation, {ReplyTo: original.ReplyTo}} {
		if err := AuthorizeClientUserInputRelation(ctx, "personal", "r", altered); err == nil {
			t.Fatal("accepted changed routing relation")
		}
	}
}

func TestClientResumeProofCannotMintNewInputOrChangeReceipt(t *testing.T) {
	input := ClientUserInput{RequestID: "client", ResumeReceiptID: "receipt"}
	if err := ValidateClientUserInput(input); err != nil {
		t.Fatal(err)
	}
	ctx := WithClientUserInput(context.Background(), "personal", input)
	if err := AuthorizeClientUserInput(ctx, "personal", "client", "main", "", "host"); err == nil {
		t.Fatal("resume intent minted a new message")
	}
	for _, target := range []struct{ plugin, request, receipt string }{{"other", "client", "receipt"}, {"personal", "other", "receipt"}, {"personal", "client", "other"}} {
		if err := AuthorizeClientInputResume(ctx, target.plugin, target.request, target.receipt, "main", "host"); err == nil {
			t.Fatal("mismatched resume admitted")
		}
	}
	if err := AuthorizeClientInputResume(ctx, "personal", "client", "receipt", "main", "host"); err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeClientInputResume(ctx, "personal", "client", "receipt", "other", "host"); err == nil {
		t.Fatal("cross session resume admitted")
	}
	input.Prompt = "new payload"
	if err := ValidateClientUserInput(input); err == nil {
		t.Fatal("mixed resume and input admitted")
	}
	admission, cancel := context.WithCancel(context.Background())
	detached := context.WithoutCancel(WithClientUserInput(admission, "personal", ClientUserInput{RequestID: "client", ResumeReceiptID: "receipt"}))
	cancel()
	if err := AuthorizeClientInputResume(detached, "personal", "client", "receipt", "main", "host"); err == nil {
		t.Fatal("detached proof revived")
	}
}
