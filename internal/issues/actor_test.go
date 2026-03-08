package issues_test

import (
	"os"
	"testing"

	"tack/internal/config"
	"tack/internal/issues"
	"tack/internal/testutil"
)

func TestResolveActorOrder(t *testing.T) {
	repo := testutil.TempRepo(t)

	err := os.MkdirAll(repo+"/.tack", 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = config.WriteDefault(repo)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("TACK_ACTOR", "")

	actor, err := issues.ResolveActor(repo, "explicit")
	if err != nil {
		t.Fatal(err)
	}

	if actor != "explicit" {
		t.Fatalf("expected explicit actor, got %q", actor)
	}

	t.Setenv("TACK_ACTOR", "env-actor")

	actor, err = issues.ResolveActor(repo, "")
	if err != nil {
		t.Fatal(err)
	}

	if actor != "env-actor" {
		t.Fatalf("expected env actor, got %q", actor)
	}

	t.Setenv("TACK_ACTOR", "")

	err = os.WriteFile(config.Path(repo), []byte("{\n  \"actor\": \"config-actor\"\n}\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	actor, err = issues.ResolveActor(repo, "")
	if err != nil {
		t.Fatal(err)
	}

	if actor != "config-actor" {
		t.Fatalf("expected config actor, got %q", actor)
	}
}
