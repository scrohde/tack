package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"tack/internal/issues"
	"tack/internal/skill"
	"tack/internal/store"
)

type App struct {
	stdout io.Writer
	stderr io.Writer
}

func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	app := &App{stdout: stdout, stderr: stderr}

	return app.Execute(ctx, args)
}

func (a *App) Execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.printUsage()

		return nil
	}

	switch args[0] {
	case "init":
		return a.runInit(args[1:])
	case "create":
		return a.runCreate(ctx, args[1:])
	case "show":
		return a.runShow(ctx, args[1:])
	case "list":
		return a.runList(ctx, args[1:])
	case "ready":
		return a.runReady(ctx, args[1:])
	case "update":
		return a.runUpdate(ctx, args[1:])
	case "edit":
		return a.runEdit(ctx, args[1:])
	case "close":
		return a.runClose(ctx, args[1:])
	case "reopen":
		return a.runReopen(ctx, args[1:])
	case "comment":
		return a.runComment(ctx, args[1:])
	case "dep":
		return a.runDep(ctx, args[1:])
	case "skill":
		return a.runSkill(args[1:])
	case "labels":
		return a.runLabels(ctx, args[1:])
	case "export":
		return a.runExport(ctx, args[1:])
	case "-h", "--help", "help":
		a.printUsage()

		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *App) runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	repoRoot, err := store.FindRepoRoot(".")
	if err != nil {
		return err
	}

	if err := store.InitRepo(repoRoot); err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, map[string]any{
			"repo_root": repoRoot,
			"db_path":   filepath.Join(repoRoot, ".tack", "issues.db"),
			"config":    filepath.Join(repoRoot, ".tack", "config.json"),
		})
	}

	_, err = fmt.Fprintf(a.stdout, "initialized tack in %s\n", filepath.Join(repoRoot, ".tack"))

	return err
}

func (a *App) runCreate(ctx context.Context, args []string) error {
	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	title := fs.String("title", "", "issue title")
	kind := fs.String("type", issues.TypeTask, "issue type")
	priority := fs.String("priority", "medium", "issue priority")
	description := fs.String("description", "", "description text")
	bodyFile := fs.String("body-file", "", "path to description body")
	parent := fs.String("parent", "", "parent issue id")
	deferredUntil := fs.String("deferred-until", "", "RFC3339 deferred until time")
	estimateMinutes := fs.String("estimate-minutes", "", "estimate in minutes")
	jsonOut := fs.Bool("json", false, "output JSON")
	actorFlag := fs.String("actor", "", "actor override")

	var (
		dependsOn multiFlag
		labels    multiFlag
	)

	fs.Var(&dependsOn, "depends-on", "blocker issue ID (repeatable)")
	fs.Var(&labels, "label", "label (repeatable)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	desc, err := readLongField(*description, *bodyFile, "")
	if err != nil {
		return err
	}

	if desc == "" && *bodyFile == "" && *description == "" {
		desc, err = issues.EditBuffer("")
		if err != nil {
			return err
		}
	}

	actor, err := issues.ResolveActor(repoRoot, *actorFlag)
	if err != nil {
		return err
	}

	input := store.CreateIssueInput{
		Title:       *title,
		Description: desc,
		Type:        *kind,
		Priority:    *priority,
		ParentID:    *parent,
		DependsOn:   dependsOn.values,
		Labels:      labels.values,
	}

	if *deferredUntil != "" {
		t, err := time.Parse(time.RFC3339, *deferredUntil)
		if err != nil {
			return fmt.Errorf("invalid --deferred-until: %w", err)
		}

		input.DeferredUntil = &t
	}

	if *estimateMinutes != "" {
		n, err := strconv.Atoi(*estimateMinutes)
		if err != nil {
			return fmt.Errorf("invalid --estimate-minutes: %w", err)
		}

		input.EstimateMinutes = &n
	}

	issue, err := s.CreateIssue(ctx, input, actor)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, issue)
	}

	return printIssueSummary(a.stdout, issue)
}

func (a *App) runSkill(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: tack skill install [--home|--path <dir>] [--json]")
	}

	switch args[0] {
	case "install":
		return a.runSkillInstall(args[1:])
	default:
		return fmt.Errorf("unknown skill command %q", args[0])
	}
}

func (a *App) runSkillInstall(args []string) error {
	fs := flag.NewFlagSet("skill install", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	home := fs.Bool("home", false, "install to $HOME/.agents/skills")
	customPath := fs.String("path", "", "custom skills root")

	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New("usage: tack skill install [--home|--path <dir>] [--json]")
	}

	if *home && strings.TrimSpace(*customPath) != "" {
		return errors.New("use only one of --home or --path")
	}

	mode := "repo"
	skillsRoot := ""

	switch {
	case *home:
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return err
		}

		mode = "home"
		skillsRoot = filepath.Join(homeDir, ".agents", "skills")
	case strings.TrimSpace(*customPath) != "":
		absPath, err := filepath.Abs(strings.TrimSpace(*customPath))
		if err != nil {
			return err
		}

		mode = "path"
		skillsRoot = absPath
	default:
		repoRoot, err := store.FindRepoRoot(".")
		if err != nil {
			return err
		}

		skillsRoot = filepath.Join(repoRoot, ".agents", "skills")
	}

	result, err := skill.InstallTackSkill(skillsRoot)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, map[string]any{
			"mode":                mode,
			"skill_name":          skill.TackSkillName,
			"skills_root":         result.SkillsRoot,
			"installed_skill_dir": result.InstalledDir,
			"installed_path":      result.InstalledPath,
		})
	}

	_, err = fmt.Fprintf(a.stdout, "installed %s skill to %s\n", skill.TackSkillName, result.InstalledPath)

	return err
}

func (a *App) runShow(ctx context.Context, args []string) error {
	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}

	_ = repoRoot

	defer s.Close()

	if len(args) == 0 {
		return errors.New("usage: tack show <id>")
	}

	id := args[0]
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New("usage: tack show <id>")
	}

	issue, err := s.GetIssue(ctx, id)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, issue)
	}

	return printIssueDetail(a.stdout, issue)
}

func (a *App) runList(ctx context.Context, args []string) error {
	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	filter, jsonOut, err := parseListFilter("list", a.stderr, args)
	if err != nil {
		return err
	}

	issues, err := s.ListIssues(ctx, filter)
	if err != nil {
		return err
	}

	if jsonOut {
		return writeJSON(a.stdout, issues)
	}

	return printIssueTable(a.stdout, issues)
}

func (a *App) runReady(ctx context.Context, args []string) error {
	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	filter, jsonOut, err := parseListFilter("ready", a.stderr, args)
	if err != nil {
		return err
	}

	issues, err := s.ReadyIssues(ctx, filter)
	if err != nil {
		return err
	}

	if jsonOut {
		return writeJSON(a.stdout, issues)
	}

	return printIssueTable(a.stdout, issues)
}

func (a *App) runUpdate(ctx context.Context, args []string) error {
	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	if len(args) == 0 {
		return errors.New("usage: tack update <id> [flags]")
	}

	id := args[0]

	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	title := &optionalString{}
	description := &optionalString{}
	bodyFile := fs.String("body-file", "", "path to description body")
	kind := &optionalString{}
	status := &optionalString{}
	priority := &optionalString{}
	assignee := &optionalString{}
	parent := &optionalString{}
	deferredUntil := &optionalString{}
	estimateMinutes := &optionalString{}
	claim := fs.Bool("claim", false, "claim issue for current actor")
	jsonOut := fs.Bool("json", false, "output JSON")
	actorFlag := fs.String("actor", "", "actor override")
	fs.Var(title, "title", "new title")
	fs.Var(description, "description", "new description")
	fs.Var(kind, "type", "new type")
	fs.Var(status, "status", "new status")
	fs.Var(priority, "priority", "new priority")
	fs.Var(assignee, "assignee", "new assignee; empty clears")
	fs.Var(parent, "parent", "new parent; empty clears")
	fs.Var(deferredUntil, "deferred-until", "RFC3339 time; empty clears")
	fs.Var(estimateMinutes, "estimate-minutes", "minutes; empty clears")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New("usage: tack update <id> [flags]")
	}

	input := store.UpdateIssueInput{Claim: *claim}
	if title.set {
		input.Title = &title.value
	}

	if description.set || *bodyFile != "" {
		desc, err := readLongField(description.value, *bodyFile, "")
		if err != nil {
			return err
		}

		input.Description = &desc
	}

	if kind.set {
		input.Type = &kind.value
	}

	if status.set {
		input.Status = &status.value
	}

	if priority.set {
		input.Priority = &priority.value
	}

	if assignee.set {
		input.Assignee = &assignee.value
	}

	if parent.set {
		input.ParentID = &parent.value
	}

	if deferredUntil.set {
		input.HasDeferredUntil = true

		if strings.TrimSpace(deferredUntil.value) != "" {
			t, err := time.Parse(time.RFC3339, deferredUntil.value)
			if err != nil {
				return fmt.Errorf("invalid --deferred-until: %w", err)
			}

			input.DeferredUntil = &t
		}
	}

	if estimateMinutes.set {
		input.HasEstimateMinutes = true

		if strings.TrimSpace(estimateMinutes.value) != "" {
			n, err := strconv.Atoi(estimateMinutes.value)
			if err != nil {
				return fmt.Errorf("invalid --estimate-minutes: %w", err)
			}

			input.EstimateMinutes = &n
		}
	}

	actor := ""
	if *claim || title.set || description.set || kind.set || status.set || priority.set || assignee.set || parent.set || deferredUntil.set || estimateMinutes.set {
		actor, err = issues.ResolveActor(repoRoot, *actorFlag)
		if err != nil {
			return err
		}
	}

	issue, err := s.UpdateIssue(ctx, id, input, actor)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, issue)
	}

	return printIssueSummary(a.stdout, issue)
}

func (a *App) runEdit(ctx context.Context, args []string) error {
	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	if len(args) == 0 {
		return errors.New("usage: tack edit <id>")
	}

	id := args[0]
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := fs.String("actor", "", "actor override")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New("usage: tack edit <id>")
	}

	current, err := s.GetIssue(ctx, id)
	if err != nil {
		return err
	}

	actor, err := issues.ResolveActor(repoRoot, *actorFlag)
	if err != nil {
		return err
	}

	edited, err := issues.EditBuffer(issues.FormatEditableIssue(current))
	if err != nil {
		return err
	}

	parsed, err := issues.ParseEditableIssue(edited)
	if err != nil {
		return err
	}

	input := store.UpdateIssueInput{
		Title:              &parsed.Title,
		Description:        &parsed.Description,
		Type:               &parsed.Type,
		Status:             &parsed.Status,
		Priority:           &parsed.Priority,
		Assignee:           &parsed.Assignee,
		ParentID:           &parsed.ParentID,
		HasDeferredUntil:   true,
		DeferredUntil:      parsed.DeferredUntil,
		HasEstimateMinutes: true,
		EstimateMinutes:    parsed.EstimateMinutes,
	}

	updated, err := s.UpdateIssue(ctx, id, input, actor)
	if err != nil {
		return err
	}

	if _, err := s.ReplaceLabels(ctx, id, parsed.Labels, actor); err != nil {
		return err
	}

	updated, err = s.GetIssue(ctx, id)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, updated)
	}

	return printIssueSummary(a.stdout, updated)
}

func (a *App) runClose(ctx context.Context, args []string) error {
	return a.runCloseLike(ctx, args, true)
}

func (a *App) runReopen(ctx context.Context, args []string) error {
	return a.runCloseLike(ctx, args, false)
}

func (a *App) runCloseLike(ctx context.Context, args []string, closeIssue bool) error {
	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	name := "close"
	if !closeIssue {
		name = "reopen"
	}

	if len(args) == 0 {
		return fmt.Errorf("usage: tack %s <id> [--reason ...]", name)
	}

	id := args[0]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	reason := fs.String("reason", "", "reason for transition")
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := fs.String("actor", "", "actor override")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return fmt.Errorf("usage: tack %s <id> [--reason ...]", name)
	}

	if closeIssue && strings.TrimSpace(*reason) == "" {
		return errors.New("--reason is required")
	}

	actor, err := issues.ResolveActor(repoRoot, *actorFlag)
	if err != nil {
		return err
	}

	var issue issues.Issue
	if closeIssue {
		issue, err = s.CloseIssue(ctx, id, *reason, actor)
	} else {
		issue, err = s.ReopenIssue(ctx, id, *reason, actor)
	}

	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, issue)
	}

	return printIssueSummary(a.stdout, issue)
}

func (a *App) runComment(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: tack comment add|list ...")
	}

	switch args[0] {
	case "add":
		return a.runCommentAdd(ctx, args[1:])
	case "list":
		return a.runCommentList(ctx, args[1:])
	default:
		return fmt.Errorf("unknown comment command %q", args[0])
	}
}

func (a *App) runCommentAdd(ctx context.Context, args []string) error {
	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	if len(args) == 0 {
		return errors.New("usage: tack comment add <id> [--body|--body-file]")
	}

	id := args[0]
	fs := flag.NewFlagSet("comment add", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	body := fs.String("body", "", "comment body")
	bodyFile := fs.String("body-file", "", "path to comment body")
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := fs.String("actor", "", "actor override")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New("usage: tack comment add <id> [--body|--body-file]")
	}

	text, err := readLongField(*body, *bodyFile, "")
	if err != nil {
		return err
	}

	if text == "" {
		text, err = issues.EditBuffer("")
		if err != nil {
			return err
		}
	}

	actor, err := issues.ResolveActor(repoRoot, *actorFlag)
	if err != nil {
		return err
	}

	comment, err := s.AddComment(ctx, id, text, actor)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, comment)
	}

	_, err = fmt.Fprintf(a.stdout, "%s %s %s\n", comment.IssueID, comment.Author, comment.CreatedAt.Format(time.RFC3339))

	return err
}

func (a *App) runCommentList(ctx context.Context, args []string) error {
	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	if len(args) == 0 {
		return errors.New("usage: tack comment list <id>")
	}

	id := args[0]
	fs := flag.NewFlagSet("comment list", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New("usage: tack comment list <id>")
	}

	comments, err := s.ListComments(ctx, id)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, comments)
	}

	for _, comment := range comments {
		if _, err := fmt.Fprintf(a.stdout, "%d\t%s\t%s\t%s\n", comment.ID, comment.CreatedAt.Format(time.RFC3339), comment.Author, comment.Body); err != nil {
			return err
		}
	}

	return nil
}

func (a *App) runDep(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: tack dep add|remove|list ...")
	}

	switch args[0] {
	case "add":
		return a.runDepAdd(ctx, args[1:])
	case "remove":
		return a.runDepRemove(ctx, args[1:])
	case "list":
		return a.runDepList(ctx, args[1:])
	default:
		return fmt.Errorf("unknown dep command %q", args[0])
	}
}

func (a *App) runDepAdd(ctx context.Context, args []string) error {
	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	if len(args) < 2 {
		return errors.New("usage: tack dep add <blocked-id> <blocker-id>")
	}

	blockedID, blockerID := args[0], args[1]
	fs := flag.NewFlagSet("dep add", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := fs.String("actor", "", "actor override")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New("usage: tack dep add <blocked-id> <blocker-id>")
	}

	actor, err := issues.ResolveActor(repoRoot, *actorFlag)
	if err != nil {
		return err
	}

	link, err := s.AddDependency(ctx, blockedID, blockerID, actor)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, link)
	}

	_, err = fmt.Fprintf(a.stdout, "%s depends on %s\n", blockedID, blockerID)

	return err
}

func (a *App) runDepRemove(ctx context.Context, args []string) error {
	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	if len(args) < 2 {
		return errors.New("usage: tack dep remove <blocked-id> <blocker-id>")
	}

	blockedID, blockerID := args[0], args[1]
	fs := flag.NewFlagSet("dep remove", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	actorFlag := fs.String("actor", "", "actor override")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New("usage: tack dep remove <blocked-id> <blocker-id>")
	}

	actor, err := issues.ResolveActor(repoRoot, *actorFlag)
	if err != nil {
		return err
	}

	if err := s.RemoveDependency(ctx, blockedID, blockerID, actor); err != nil {
		return err
	}

	_, err = fmt.Fprintf(a.stdout, "removed dependency %s -> %s\n", blockerID, blockedID)

	return err
}

func (a *App) runDepList(ctx context.Context, args []string) error {
	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	if len(args) == 0 {
		return errors.New("usage: tack dep list <id>")
	}

	id := args[0]
	fs := flag.NewFlagSet("dep list", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New("usage: tack dep list <id>")
	}

	list, err := s.ListDependencies(ctx, id)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, list)
	}

	_, err = fmt.Fprintf(a.stdout, "blocked_by=%d blocks=%d\n", len(list.BlockedBy), len(list.Blocks))

	return err
}

func (a *App) runLabels(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: tack labels add|remove|list ...")
	}

	switch args[0] {
	case "add":
		return a.runLabelsAdd(ctx, args[1:])
	case "remove":
		return a.runLabelsRemove(ctx, args[1:])
	case "list":
		return a.runLabelsList(ctx, args[1:])
	default:
		return fmt.Errorf("unknown labels command %q", args[0])
	}
}

func (a *App) runLabelsAdd(ctx context.Context, args []string) error {
	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	if len(args) == 0 {
		return errors.New("usage: tack labels add <id> <label> [label...]")
	}

	id := args[0]
	fs := flag.NewFlagSet("labels add", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := fs.String("actor", "", "actor override")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return errors.New("usage: tack labels add <id> <label> [label...]")
	}

	actor, err := issues.ResolveActor(repoRoot, *actorFlag)
	if err != nil {
		return err
	}

	labels, err := s.AddLabels(ctx, id, fs.Args(), actor)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, labels)
	}

	_, err = fmt.Fprintln(a.stdout, strings.Join(labels, "\n"))

	return err
}

func (a *App) runLabelsRemove(ctx context.Context, args []string) error {
	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	if len(args) == 0 {
		return errors.New("usage: tack labels remove <id> <label> [label...]")
	}

	id := args[0]
	fs := flag.NewFlagSet("labels remove", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := fs.String("actor", "", "actor override")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return errors.New("usage: tack labels remove <id> <label> [label...]")
	}

	actor, err := issues.ResolveActor(repoRoot, *actorFlag)
	if err != nil {
		return err
	}

	labels, err := s.RemoveLabels(ctx, id, fs.Args(), actor)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, labels)
	}

	_, err = fmt.Fprintln(a.stdout, strings.Join(labels, "\n"))

	return err
}

func (a *App) runLabelsList(ctx context.Context, args []string) error {
	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	if len(args) == 0 {
		return errors.New("usage: tack labels list <id>")
	}

	id := args[0]
	fs := flag.NewFlagSet("labels list", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New("usage: tack labels list <id>")
	}

	labels, err := s.ListLabels(ctx, id)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, labels)
	}

	_, err = fmt.Fprintln(a.stdout, strings.Join(labels, "\n"))

	return err
}

func (a *App) runExport(ctx context.Context, args []string) error {
	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer s.Close()

	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	jsonOut := fs.Bool("json", true, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if !*jsonOut {
		return errors.New("export only supports JSON")
	}

	data, err := s.Export(ctx)
	if err != nil {
		return err
	}

	return writeJSON(a.stdout, data)
}

func parseListFilter(name string, stderr io.Writer, args []string) (store.ListFilter, bool, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	status := fs.String("status", "", "status filter")
	assignee := fs.String("assignee", "", "assignee filter")
	label := fs.String("label", "", "label filter")
	kind := fs.String("type", "", "type filter")
	limit := fs.Int("limit", 0, "max results")

	jsonOut := fs.Bool("json", false, "output JSON")

	err := fs.Parse(args)
	if err != nil {
		return store.ListFilter{}, false, err
	}

	if fs.NArg() != 0 {
		return store.ListFilter{}, false, fmt.Errorf("usage: tack %s [flags]", name)
	}

	return store.ListFilter{
		Status:   strings.TrimSpace(*status),
		Assignee: strings.TrimSpace(*assignee),
		Label:    strings.TrimSpace(*label),
		Type:     strings.TrimSpace(*kind),
		Limit:    *limit,
	}, *jsonOut, nil
}

func openRepoStore() (string, *store.Store, error) {
	repoRoot, err := store.FindRepoRoot(".")
	if err != nil {
		return "", nil, err
	}

	if err := store.EnsureInitialized(repoRoot); err != nil {
		return "", nil, err
	}

	s, err := store.Open(filepath.Join(repoRoot, ".tack", "issues.db"))
	if err != nil {
		return "", nil, err
	}

	return repoRoot, s, nil
}

func readLongField(inline, bodyFile, fallback string) (string, error) {
	if strings.TrimSpace(inline) != "" && strings.TrimSpace(bodyFile) != "" {
		return "", errors.New("use only one of inline body or body-file")
	}

	if strings.TrimSpace(bodyFile) != "" {
		data, err := os.ReadFile(bodyFile)
		if err != nil {
			return "", err
		}

		return strings.TrimSpace(string(data)), nil
	}

	if strings.TrimSpace(inline) != "" {
		return strings.TrimSpace(inline), nil
	}

	return strings.TrimSpace(fallback), nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)

	return enc.Encode(v)
}

func printIssueSummary(w io.Writer, issue issues.Issue) error {
	_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", issue.ID, issue.Status, issue.Type, issue.Title)

	return err
}

func printIssueDetail(w io.Writer, issue issues.Issue) error {
	lines := []string{
		"id: " + issue.ID,
		fmt.Sprintf("sequence: %d", issue.Sequence),
		"title: " + issue.Title,
		"type: " + issue.Type,
		"status: " + issue.Status,
		"priority: " + issue.Priority,
		"assignee: " + issue.Assignee,
		"parent: " + issue.ParentID,
		"labels: " + strings.Join(issue.Labels, ","),
		"description:",
		issue.Description,
	}
	_, err := fmt.Fprintln(w, strings.Join(lines, "\n"))

	return err
}

func printIssueTable(w io.Writer, all []issues.Issue) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tSTATUS\tTYPE\tPRIORITY\tTITLE"); err != nil {
		return err
	}

	for _, issue := range all {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", issue.ID, issue.Status, issue.Type, issue.Priority, issue.Title); err != nil {
			return err
		}
	}

	return tw.Flush()
}

func (a *App) printUsage() {
	fmt.Fprintln(a.stdout, `tack commands:
  tack init
  tack create
  tack show <id>
  tack list
  tack ready
  tack update <id>
  tack edit <id>
  tack close <id>
  tack reopen <id>
  tack comment add|list
  tack dep add|remove|list
  tack skill install
  tack labels add|remove|list
  tack export --json`)
}

type multiFlag struct {
	values []string
}

func (f *multiFlag) String() string {
	return strings.Join(f.values, ",")
}

func (f *multiFlag) Set(v string) error {
	f.values = append(f.values, v)

	return nil
}

type optionalString struct {
	value string
	set   bool
}

func (o *optionalString) String() string {
	return o.value
}

func (o *optionalString) Set(v string) error {
	o.set = true
	o.value = v

	return nil
}
