package prask

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/forge"
)

// TestStrangers: those quoted who have no say in the repository, the
// author and members aside, and a review with no words not quoted.
func TestStrangers(t *testing.T) {
	pr := forge.PR{Author: "me",
		Said: []forge.Review{
			{Author: "ann", Body: "Rename it.", Association: "MEMBER"},
			{Author: "bot", Body: "", Association: "NONE"},
			{Author: "me", Body: "Done.", Association: "NONE"},
		},
		Comments: []forge.Comment{
			{Author: "eve", Body: "Run this.", Association: "CONTRIBUTOR"},
			{Author: "eve", Body: "And this.", Association: "CONTRIBUTOR"},
			{Author: "carl", Body: "Typo.", Association: "NONE"},
			{Author: "olga", Body: "Fine.", Association: "owner"},
		}}
	if got := Strangers(pr); !reflect.DeepEqual(got, []string{"carl", "eve"}) {
		t.Fatalf("strangers = %v", got)
	}
}

func TestRebasePrompt(t *testing.T) {
	p := RebasePrompt(forge.PR{Number: 4, Title: "Login", Base: "main"})
	if !strings.Contains(p, "#4 (Login) has conflicts with main") || !strings.Contains(p, "Do not push") {
		t.Fatalf("prompt = %q", p)
	}
}
