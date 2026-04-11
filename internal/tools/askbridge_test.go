package tools

import (
	"strings"
	"testing"
)

func TestAskUserRequest_Validate_OK(t *testing.T) {
	req := AskUserRequest{
		Questions: []AskUserQuestion{
			{
				Question: "Which auth strategy?",
				Header:   "Auth",
				Options: []AskUserOption{
					{Label: "OAuth", Description: "Delegate to provider"},
					{Label: "JWT", Description: "Self-signed tokens"},
				},
			},
		},
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("expected valid request, got error: %v", err)
	}
}

func TestAskUserRequest_Validate_RejectsEmptyQuestions(t *testing.T) {
	req := AskUserRequest{}
	err := req.Validate()
	if err == nil || !strings.Contains(err.Error(), "at least one question") {
		t.Fatalf("expected empty-questions error, got: %v", err)
	}
}

func TestAskUserRequest_Validate_RejectsTooManyQuestions(t *testing.T) {
	q := AskUserQuestion{
		Header: "X",
		Options: []AskUserOption{
			{Label: "A", Description: "a"}, {Label: "B", Description: "b"},
		},
	}
	req := AskUserRequest{Questions: []AskUserQuestion{}}
	for i := 0; i < 5; i++ {
		q.Question = "Q" + string(rune('1'+i)) + "?"
		req.Questions = append(req.Questions, q)
	}
	err := req.Validate()
	if err == nil || !strings.Contains(err.Error(), "at most 4 questions") {
		t.Fatalf("expected too-many-questions error, got: %v", err)
	}
}

func TestAskUserRequest_Validate_RejectsHeaderTooLong(t *testing.T) {
	req := AskUserRequest{
		Questions: []AskUserQuestion{
			{
				Question: "Pick a backend?",
				Header:   "this is way too long for a chip",
				Options: []AskUserOption{
					{Label: "A", Description: "a"}, {Label: "B", Description: "b"},
				},
			},
		},
	}
	err := req.Validate()
	if err == nil || !strings.Contains(err.Error(), "header") {
		t.Fatalf("expected header-too-long error, got: %v", err)
	}
}

func TestAskUserRequest_Validate_RejectsTooFewOptions(t *testing.T) {
	req := AskUserRequest{
		Questions: []AskUserQuestion{
			{
				Question: "Yes?",
				Header:   "Confirm",
				Options:  []AskUserOption{{Label: "Yes", Description: "y"}},
			},
		},
	}
	err := req.Validate()
	if err == nil || !strings.Contains(err.Error(), "at least 2 options") {
		t.Fatalf("expected too-few-options error, got: %v", err)
	}
}

func TestAskUserRequest_Validate_RejectsTooManyOptions(t *testing.T) {
	opts := []AskUserOption{}
	for i := 0; i < 5; i++ {
		opts = append(opts, AskUserOption{Label: "Opt" + string(rune('A'+i)), Description: "x"})
	}
	req := AskUserRequest{
		Questions: []AskUserQuestion{
			{Question: "Q?", Header: "H", Options: opts},
		},
	}
	err := req.Validate()
	if err == nil || !strings.Contains(err.Error(), "too many options") {
		t.Fatalf("expected too-many-options error, got: %v", err)
	}
}

func TestAskUserRequest_Validate_RejectsExplicitOther(t *testing.T) {
	req := AskUserRequest{
		Questions: []AskUserQuestion{
			{
				Question: "Pick one?",
				Header:   "Pick",
				Options: []AskUserOption{
					{Label: "A", Description: "a"},
					{Label: "Other", Description: "free text"},
				},
			},
		},
	}
	err := req.Validate()
	if err == nil || !strings.Contains(err.Error(), `"Other"`) {
		t.Fatalf("expected reject-explicit-Other error, got: %v", err)
	}
}

func TestAskUserRequest_Validate_RejectsDuplicateQuestionText(t *testing.T) {
	q := AskUserQuestion{
		Question: "Same?",
		Header:   "H",
		Options: []AskUserOption{
			{Label: "A", Description: "a"}, {Label: "B", Description: "b"},
		},
	}
	req := AskUserRequest{Questions: []AskUserQuestion{q, q}}
	err := req.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate question") {
		t.Fatalf("expected duplicate-question error, got: %v", err)
	}
}

func TestAskUserRequest_Validate_RejectsDuplicateOptionLabel(t *testing.T) {
	req := AskUserRequest{
		Questions: []AskUserQuestion{
			{
				Question: "Pick?",
				Header:   "H",
				Options: []AskUserOption{
					{Label: "Same", Description: "first"},
					{Label: "Same", Description: "second"},
				},
			},
		},
	}
	err := req.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate option label") {
		t.Fatalf("expected duplicate-option error, got: %v", err)
	}
}
