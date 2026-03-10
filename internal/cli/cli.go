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

const (
	usageSkillInstall = "usage: tack skill install [--home|--path <dir>] [--json]"
	usageShow         = "usage: tack show <id>"
	usageUpdate       = "usage: tack update <id> [flags]"
	usageEdit         = "usage: tack edit <id>"
	usageComment      = "usage: tack comment add|list"
	usageCommentAdd   = "usage: tack comment add <id> [--body|--body-file]"
	usageCommentList  = "usage: tack comment list <id>"
	usageDep          = "usage: tack dep add|remove|list"
	usageDepAdd       = "usage: tack dep add <blocked-id> <blocker-id>"
	usageDepRemove    = "usage: tack dep remove <blocked-id> <blocker-id>"
	usageDepList      = "usage: tack dep list <id>"
	usageLabels       = "usage: tack labels add|remove|list"
	usageLabelsAdd    = "usage: tack labels add <id> <label> [label...]"
	usageLabelsRemove = "usage: tack labels remove <id> <label> [label...]"
	usageLabelsList   = "usage: tack labels list <id>"
)

func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	app := &App{stdout: stdout, stderr: stderr}

	return app.Execute(ctx, args)
}

func (a *App) Execute(ctx context.Context, args []string) error {
	err := a.execute(ctx, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}

	return err
}

func (a *App) execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		err := a.printUsage()
		if err != nil {
			return err
		}

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
	case "-h", "--help":
		err := a.printUsage()
		if err != nil {
			return err
		}

		return nil
	case "help":
		return a.runHelp(ctx, args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *App) runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	err := a.parseFlags(fs, args, "usage: tack init [--json]")
	if err != nil {
		return err
	}

	repoRoot, err := store.FindRepoRoot(".")
	if err != nil {
		return err
	}

	err = store.InitRepo(repoRoot)
	if err != nil {
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
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
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

	err := a.parseFlags(fs, args, "usage: tack create [flags]")
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New("usage: tack create [flags]")
	}

	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

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
		return errors.New(usageSkillInstall)
	}

	if isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageSkillInstall, nil)
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
	home := fs.Bool("home", false, "install to $HOME/.agents/skills")
	customPath := fs.String("path", "", "custom skills root")

	jsonOut := fs.Bool("json", false, "output JSON")

	err := a.parseFlags(fs, args, usageSkillInstall)
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New(usageSkillInstall)
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
	if len(args) > 0 && isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageShow, nil)
	}

	if len(args) == 0 {
		return errors.New(usageShow)
	}

	id := args[0]
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	err := a.parseFlags(fs, args[1:], usageShow)
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New(usageShow)
	}

	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

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
	filter, jsonOut, summaryOut, err := parseListFilter("list", a.stdout, args)
	if err != nil {
		return err
	}

	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

	if summaryOut {
		summaries, err := s.ListIssueSummaries(ctx, filter)
		if err != nil {
			return err
		}

		return writeJSON(a.stdout, summaries)
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
	filter, jsonOut, summaryOut, err := parseListFilter("ready", a.stdout, args)
	if err != nil {
		return err
	}

	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

	if summaryOut {
		summaries, err := s.ReadyIssueSummaries(ctx, filter)
		if err != nil {
			return err
		}

		return writeJSON(a.stdout, summaries)
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
	if len(args) > 0 && isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageUpdate, nil)
	}

	if len(args) == 0 {
		return errors.New(usageUpdate)
	}

	id := args[0]
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
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

	err := a.parseFlags(fs, args[1:], usageUpdate)
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New(usageUpdate)
	}

	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

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
	if len(args) > 0 && isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageEdit, nil)
	}

	if len(args) == 0 {
		return errors.New(usageEdit)
	}

	id := args[0]
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := fs.String("actor", "", "actor override")

	err := a.parseFlags(fs, args[1:], usageEdit)
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New(usageEdit)
	}

	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

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

	_, err = s.UpdateIssue(ctx, id, input, actor)
	if err != nil {
		return err
	}

	_, err = s.ReplaceLabels(ctx, id, parsed.Labels, actor)
	if err != nil {
		return err
	}

	updated, err := s.GetIssue(ctx, id)
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
	name := "close"
	if !closeIssue {
		name = "reopen"
	}

	usage := fmt.Sprintf("usage: tack %s <id> [--reason ...]", name)
	if len(args) > 0 && isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usage, nil)
	}

	if len(args) == 0 {
		return errors.New(usage)
	}

	id := args[0]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	reason := fs.String("reason", "", "reason for transition")
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := fs.String("actor", "", "actor override")

	err := a.parseFlags(fs, args[1:], usage)
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New(usage)
	}

	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

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
		return errors.New(usageComment)
	}

	if isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageComment, nil)
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
	if len(args) > 0 && isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageCommentAdd, nil)
	}

	if len(args) == 0 {
		return errors.New(usageCommentAdd)
	}

	id := args[0]
	fs := flag.NewFlagSet("comment add", flag.ContinueOnError)
	body := fs.String("body", "", "comment body")
	bodyFile := fs.String("body-file", "", "path to comment body")
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := fs.String("actor", "", "actor override")

	err := a.parseFlags(fs, args[1:], usageCommentAdd)
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New(usageCommentAdd)
	}

	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

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
	if len(args) > 0 && isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageCommentList, nil)
	}

	if len(args) == 0 {
		return errors.New(usageCommentList)
	}

	id := args[0]
	fs := flag.NewFlagSet("comment list", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	err := a.parseFlags(fs, args[1:], usageCommentList)
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New(usageCommentList)
	}

	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

	comments, err := s.ListComments(ctx, id)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, comments)
	}

	for _, comment := range comments {
		_, err = fmt.Fprintf(a.stdout, "%d\t%s\t%s\t%s\n", comment.ID, comment.CreatedAt.Format(time.RFC3339), comment.Author, comment.Body)
		if err != nil {
			return err
		}
	}

	return nil
}

func (a *App) runDep(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(usageDep)
	}

	if isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageDep, nil)
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
	if len(args) > 0 && isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageDepAdd, nil)
	}

	if len(args) < 2 {
		return errors.New(usageDepAdd)
	}

	blockedID, blockerID := args[0], args[1]
	fs := flag.NewFlagSet("dep add", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := fs.String("actor", "", "actor override")

	err := a.parseFlags(fs, args[2:], usageDepAdd)
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New(usageDepAdd)
	}

	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

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
	if len(args) > 0 && isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageDepRemove, nil)
	}

	if len(args) < 2 {
		return errors.New(usageDepRemove)
	}

	blockedID, blockerID := args[0], args[1]
	fs := flag.NewFlagSet("dep remove", flag.ContinueOnError)
	actorFlag := fs.String("actor", "", "actor override")

	err := a.parseFlags(fs, args[2:], usageDepRemove)
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New(usageDepRemove)
	}

	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

	actor, err := issues.ResolveActor(repoRoot, *actorFlag)
	if err != nil {
		return err
	}

	err = s.RemoveDependency(ctx, blockedID, blockerID, actor)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(a.stdout, "removed dependency %s -> %s\n", blockerID, blockedID)

	return err
}

func (a *App) runDepList(ctx context.Context, args []string) error {
	if len(args) > 0 && isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageDepList, nil)
	}

	if len(args) == 0 {
		return errors.New(usageDepList)
	}

	id := args[0]
	fs := flag.NewFlagSet("dep list", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	err := a.parseFlags(fs, args[1:], usageDepList)
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New(usageDepList)
	}

	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

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
		return errors.New(usageLabels)
	}

	if isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageLabels, nil)
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
	if len(args) > 0 && isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageLabelsAdd, nil)
	}

	if len(args) == 0 {
		return errors.New(usageLabelsAdd)
	}

	id := args[0]
	fs := flag.NewFlagSet("labels add", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := fs.String("actor", "", "actor override")

	err := a.parseFlags(fs, args[1:], usageLabelsAdd)
	if err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return errors.New(usageLabelsAdd)
	}

	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

	labelsToAdd := fs.Args()

	actor, err := issues.ResolveActor(repoRoot, *actorFlag)
	if err != nil {
		return err
	}

	labels, err := s.AddLabels(ctx, id, labelsToAdd, actor)
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
	if len(args) > 0 && isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageLabelsRemove, nil)
	}

	if len(args) == 0 {
		return errors.New(usageLabelsRemove)
	}

	id := args[0]
	fs := flag.NewFlagSet("labels remove", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := fs.String("actor", "", "actor override")

	err := a.parseFlags(fs, args[1:], usageLabelsRemove)
	if err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return errors.New(usageLabelsRemove)
	}

	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

	labelsToRemove := fs.Args()

	actor, err := issues.ResolveActor(repoRoot, *actorFlag)
	if err != nil {
		return err
	}

	labels, err := s.RemoveLabels(ctx, id, labelsToRemove, actor)
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
	if len(args) > 0 && isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usageLabelsList, nil)
	}

	if len(args) == 0 {
		return errors.New(usageLabelsList)
	}

	id := args[0]
	fs := flag.NewFlagSet("labels list", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	err := a.parseFlags(fs, args[1:], usageLabelsList)
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New(usageLabelsList)
	}

	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

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
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	jsonOut := fs.Bool("json", true, "output JSON")

	err := a.parseFlags(fs, args, "usage: tack export --json")
	if err != nil {
		return err
	}

	if !*jsonOut {
		return errors.New("export only supports JSON")
	}

	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

	data, err := s.Export(ctx)
	if err != nil {
		return err
	}

	return writeJSON(a.stdout, data)
}

func parseListFilter(name string, helpOutput io.Writer, args []string) (store.ListFilter, bool, bool, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	status := fs.String("status", "", "status filter")
	assignee := fs.String("assignee", "", "assignee filter")
	label := fs.String("label", "", "label filter")
	kind := fs.String("type", "", "type filter")
	limit := fs.Int("limit", 0, "max results")

	jsonOut := fs.Bool("json", false, "output JSON")
	summaryOut := fs.Bool("summary", false, "output compact JSON summaries")

	err := parseFlagSet(fs, helpOutput, args, fmt.Sprintf("usage: tack %s [flags]", name))
	if err != nil {
		return store.ListFilter{}, false, false, err
	}

	if fs.NArg() != 0 {
		return store.ListFilter{}, false, false, fmt.Errorf("usage: tack %s [flags]", name)
	}

	if *summaryOut && !*jsonOut {
		return store.ListFilter{}, false, false, errors.New("--summary requires --json")
	}

	return store.ListFilter{
		Status:   strings.TrimSpace(*status),
		Assignee: strings.TrimSpace(*assignee),
		Label:    strings.TrimSpace(*label),
		Type:     strings.TrimSpace(*kind),
		Limit:    *limit,
	}, *jsonOut, *summaryOut, nil
}

func openRepoStore() (string, *store.Store, error) {
	repoRoot, err := store.FindRepoRoot(".")
	if err != nil {
		return "", nil, err
	}

	err = store.EnsureInitialized(repoRoot)
	if err != nil {
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

	_, err := fmt.Fprintln(tw, "ID\tSTATUS\tTYPE\tPRIORITY\tTITLE")
	if err != nil {
		return err
	}

	for _, issue := range all {
		_, err = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", issue.ID, issue.Status, issue.Type, issue.Priority, issue.Title)
		if err != nil {
			return err
		}
	}

	return tw.Flush()
}

func (a *App) printUsage() error {
	_, err := fmt.Fprintln(a.stdout, `tack commands:
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
  tack skill install [--home|--path <dir>]
  tack labels add|remove|list
  tack export --json`)

	return err
}

func (a *App) runHelp(ctx context.Context, args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		return a.printUsage()
	}

	withHelp := append(args[1:], "--help")

	switch args[0] {
	case "init":
		return a.runInit(withHelp)
	case "create":
		return a.runCreate(ctx, withHelp)
	case "show":
		return a.runShow(ctx, withHelp)
	case "list":
		return a.runList(ctx, withHelp)
	case "ready":
		return a.runReady(ctx, withHelp)
	case "update":
		return a.runUpdate(ctx, withHelp)
	case "edit":
		return a.runEdit(ctx, withHelp)
	case "close":
		return a.runClose(ctx, withHelp)
	case "reopen":
		return a.runReopen(ctx, withHelp)
	case "comment":
		return a.runComment(ctx, withHelp)
	case "dep":
		return a.runDep(ctx, withHelp)
	case "skill":
		return a.runSkill(withHelp)
	case "labels":
		return a.runLabels(ctx, withHelp)
	case "export":
		return a.runExport(ctx, withHelp)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *App) parseFlags(fs *flag.FlagSet, args []string, usage string) error {
	return parseFlagSet(fs, a.stdout, args, usage)
}

func parseFlagSet(fs *flag.FlagSet, helpOutput io.Writer, args []string, usage string) error {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	err := fs.Parse(args)
	if err == nil {
		return nil
	}

	if errors.Is(err, flag.ErrHelp) {
		usageErr := printFlagUsage(helpOutput, usage, fs)
		if usageErr != nil {
			return usageErr
		}

		return flag.ErrHelp
	}

	return err
}

func printFlagUsage(w io.Writer, usage string, fs *flag.FlagSet) error {
	return printUsageWithDefaults(w, usage, func(out io.Writer) error {
		hasFlags := false

		fs.VisitAll(func(*flag.Flag) {
			hasFlags = true
		})

		if !hasFlags {
			return nil
		}

		previousOutput := fs.Output()
		fs.SetOutput(out)
		fs.PrintDefaults()
		fs.SetOutput(previousOutput)

		return nil
	})
}

func (a *App) printCommandUsage(w io.Writer, usage string, defaults func(io.Writer) error) error {
	return printUsageWithDefaults(w, usage, defaults)
}

func printUsageWithDefaults(w io.Writer, usage string, defaults func(io.Writer) error) error {
	_, err := fmt.Fprintln(w, usage)
	if err != nil {
		return err
	}

	if defaults == nil {
		return nil
	}

	_, err = fmt.Fprintln(w)
	if err != nil {
		return err
	}

	return defaults(w)
}

func isHelpToken(arg string) bool {
	return arg == "-h" || arg == "--help"
}

func closeStore(s *store.Store) {
	err := s.Close()
	if err != nil {
		return
	}
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
