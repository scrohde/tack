package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"tack/internal/issues"
	"tack/internal/skill"
	"tack/internal/store"
	"tack/internal/tui"
)

type App struct {
	stdout io.Writer
	stderr io.Writer
}

type (
	commandHandler    func([]string) error
	subcommandHandler func([]string) error
)

const (
	usageSkillInstall = "usage: tack skill install [--home|--path <dir>] [--json]"
	usageImport       = "usage: tack import --file <path> [--json]"
	usageShow         = "usage: tack show <id> [--json]"
	usageTUI          = "usage: tack tui [--ready] [--status <status>] [--type <type>] [--label <label>] [--assignee <assignee>] [--limit <n>]"
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

var launchTUI = tui.Run

type importFileKind string

const (
	importFileKindManifest importFileKind = "manifest"
	importFileKindSnapshot importFileKind = "snapshot"
)

type importFile struct {
	kind     importFileKind
	manifest store.ImportManifest
	snapshot issues.Export
}

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

	if isHelpToken(args[0]) {
		err := a.printUsage()
		if err != nil {
			return err
		}

		return nil
	}

	if args[0] == "help" {
		return a.runHelp(ctx, args[1:])
	}

	handler, ok := a.commandHandlers(ctx)[args[0]]
	if !ok {
		return fmt.Errorf("unknown command %q", args[0])
	}

	return handler(args[1:])
}

func (a *App) commandHandlers(ctx context.Context) map[string]commandHandler {
	return map[string]commandHandler{
		"init":   a.runInit,
		"create": func(args []string) error { return a.runCreate(ctx, args) },
		"import": func(args []string) error { return a.runImport(ctx, args) },
		"show":   func(args []string) error { return a.runShow(ctx, args) },
		"tui":    func(args []string) error { return a.runTUI(ctx, args) },
		"list":   func(args []string) error { return a.runList(ctx, args) },
		"ready":  func(args []string) error { return a.runReady(ctx, args) },
		"update": func(args []string) error { return a.runUpdate(ctx, args) },
		"edit":   func(args []string) error { return a.runEdit(ctx, args) },
		"close":  func(args []string) error { return a.runClose(ctx, args) },
		"reopen": func(args []string) error { return a.runReopen(ctx, args) },
		"comment": func(args []string) error {
			return a.runComment(ctx, args)
		},
		"dep":   func(args []string) error { return a.runDep(ctx, args) },
		"skill": a.runSkill,
		"labels": func(args []string) error {
			return a.runLabels(ctx, args)
		},
		"export": func(args []string) error { return a.runExport(ctx, args) },
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

	installResult, err := skill.InstallTackSkill(filepath.Join(repoRoot, ".agents", "skills"))
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, map[string]any{
			"repo_root":           repoRoot,
			"db_path":             filepath.Join(repoRoot, ".tack", "issues.db"),
			"config":              filepath.Join(repoRoot, ".tack", "config.json"),
			"skill_name":          skill.TackSkillName,
			"skills_root":         installResult.SkillsRoot,
			"installed_skill_dir": installResult.InstalledDir,
			"installed_path":      installResult.InstalledPath,
		})
	}

	_, err = fmt.Fprintf(
		a.stdout,
		"initialized tack in %s\ninstalled %s skill to %s\n",
		filepath.Join(repoRoot, ".tack"),
		skill.TackSkillName,
		installResult.InstalledPath,
	)

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
	jsonOut := fs.Bool("json", false, "output JSON")
	actorFlag := addActorFlag(fs)

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

	actor, err := resolveActor(repoRoot, actorFlag)
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
	return a.runSubcommand(args, usageSkillInstall, "unknown skill command %q", map[string]subcommandHandler{
		"install": a.runSkillInstall,
	})
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

func (a *App) runImport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	filePath := fs.String("file", "", "path to JSON manifest")
	jsonOut := fs.Bool("json", false, "output JSON")
	actorFlag := addActorFlag(fs)

	err := a.parseFlags(fs, args, usageImport)
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New(usageImport)
	}

	if strings.TrimSpace(*filePath) == "" {
		return errors.New("--file is required")
	}

	input, err := readImportFile(*filePath)
	if err != nil {
		return err
	}

	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

	var result store.ImportResult

	switch input.kind {
	case importFileKindSnapshot:
		result, err = s.ImportSnapshot(ctx, input.snapshot)
		if err != nil {
			return err
		}
	default:
		actor, err := resolveActor(repoRoot, actorFlag)
		if err != nil {
			return err
		}

		result, err = s.ImportIssues(ctx, input.manifest, actor)
		if err != nil {
			return err
		}
	}

	if *jsonOut {
		return writeJSON(a.stdout, result)
	}

	return printImportSummary(a.stdout, input, result)
}

func (a *App) runShow(ctx context.Context, args []string) error {
	leading, remaining, err := a.parseLeadingArgs(args, usageShow, 1)
	if err != nil {
		return err
	}

	id := leading[0]
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	err = a.parseFlags(fs, remaining, usageShow)
	if err != nil {
		return err
	}

	err = requireNoExtraArgs(fs, usageShow)
	if err != nil {
		return err
	}

	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

	issue, err := s.GetIssueDetail(ctx, id)
	if err != nil {
		return err
	}

	if *jsonOut {
		return writeJSON(a.stdout, issue)
	}

	return printIssueDetail(a.stdout, issue)
}

func (a *App) runTUI(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	ready := fs.Bool("ready", false, "start in ready view")
	status := fs.String("status", "", "status filter")
	kind := fs.String("type", "", "type filter")
	label := fs.String("label", "", "label filter")
	assignee := fs.String("assignee", "", "assignee filter")
	limit := fs.Int("limit", 0, "limit results")

	err := parseTUIFlagSet(fs, a.stdout, args)
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New(usageTUI)
	}

	options := tui.StartupOptions{
		Source: tui.DataSourceAll,
		Filter: store.ListFilter{
			Statuses:  singleValueFilter(strings.TrimSpace(*status)),
			Assignees: singleValueFilter(strings.TrimSpace(*assignee)),
			Labels:    singleValueFilter(strings.TrimSpace(*label)),
			Types:     singleValueFilter(strings.TrimSpace(*kind)),
			Limit:     *limit,
		},
	}

	if *ready {
		options.Source = tui.DataSourceReady
	}

	return launchTUI(ctx, a.stdout, a.stderr, options)
}

func (a *App) runList(ctx context.Context, args []string) error {
	filter, jsonOut, summaryOut, err := parseListFilter("list", a.stdout, args, true)
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
	filter, jsonOut, summaryOut, err := parseListFilter("ready", a.stdout, args, false)
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
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	title := &optionalString{}
	description := &optionalString{}
	bodyFile := fs.String("body-file", "", "path to description body")
	kind := &optionalString{}
	status := &optionalString{}
	priority := &optionalString{}
	assignee := &optionalString{}
	parent := &optionalString{}
	claim := fs.Bool("claim", false, "claim issue for current actor")
	jsonOut := fs.Bool("json", false, "output JSON")
	actorFlag := addActorFlag(fs)
	fs.Var(title, "title", "new title")
	fs.Var(description, "description", "new description")
	fs.Var(kind, "type", "new type")
	fs.Var(status, "status", "new status")
	fs.Var(priority, "priority", "new priority")
	fs.Var(assignee, "assignee", "new assignee; empty clears")
	fs.Var(parent, "parent", "new parent; empty clears")

	if len(args) > 0 && isHelpToken(args[0]) {
		return printUpdateUsage(a.stdout, fs)
	}

	leading, remaining, err := a.parseLeadingArgs(args, usageUpdate, 1)
	if err != nil {
		return err
	}

	id := leading[0]

	err = parseUpdateFlagSet(fs, a.stdout, remaining)
	if err != nil {
		return err
	}

	err = requireNoExtraArgs(fs, usageUpdate)
	if err != nil {
		return err
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

	actor := ""
	if *claim || title.set || description.set || kind.set || status.set || priority.set || assignee.set || parent.set {
		actor, err = resolveActor(repoRoot, actorFlag)
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
	leading, remaining, err := a.parseLeadingArgs(args, usageEdit, 1)
	if err != nil {
		return err
	}

	id := leading[0]
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := addActorFlag(fs)

	err = a.parseFlags(fs, remaining, usageEdit)
	if err != nil {
		return err
	}

	err = requireNoExtraArgs(fs, usageEdit)
	if err != nil {
		return err
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

	actor, err := resolveActor(repoRoot, actorFlag)
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
		Title:       &parsed.Title,
		Description: &parsed.Description,
		Type:        &parsed.Type,
		Status:      &parsed.Status,
		Priority:    &parsed.Priority,
		Assignee:    &parsed.Assignee,
		ParentID:    &parsed.ParentID,
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

	leading, remaining, err := a.parseLeadingArgs(args, usage, 1)
	if err != nil {
		return err
	}

	id := leading[0]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	reason := fs.String("reason", "", "reason for transition")
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := addActorFlag(fs)

	err = a.parseFlags(fs, remaining, usage)
	if err != nil {
		return err
	}

	err = requireNoExtraArgs(fs, usage)
	if err != nil {
		return err
	}

	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

	if closeIssue && strings.TrimSpace(*reason) == "" {
		return errors.New("--reason is required")
	}

	actor, err := resolveActor(repoRoot, actorFlag)
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
	return a.runSubcommand(args, usageComment, "unknown comment command %q", map[string]subcommandHandler{
		"add":  func(args []string) error { return a.runCommentAdd(ctx, args) },
		"list": func(args []string) error { return a.runCommentList(ctx, args) },
	})
}

func (a *App) runCommentAdd(ctx context.Context, args []string) error {
	leading, remaining, err := a.parseLeadingArgs(args, usageCommentAdd, 1)
	if err != nil {
		return err
	}

	id := leading[0]
	fs := flag.NewFlagSet("comment add", flag.ContinueOnError)
	body := fs.String("body", "", "comment body")
	bodyFile := fs.String("body-file", "", "path to comment body")
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := addActorFlag(fs)

	err = a.parseFlags(fs, remaining, usageCommentAdd)
	if err != nil {
		return err
	}

	err = requireNoExtraArgs(fs, usageCommentAdd)
	if err != nil {
		return err
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

	actor, err := resolveActor(repoRoot, actorFlag)
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
	leading, remaining, err := a.parseLeadingArgs(args, usageCommentList, 1)
	if err != nil {
		return err
	}

	id := leading[0]
	fs := flag.NewFlagSet("comment list", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	err = a.parseFlags(fs, remaining, usageCommentList)
	if err != nil {
		return err
	}

	err = requireNoExtraArgs(fs, usageCommentList)
	if err != nil {
		return err
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
	return a.runSubcommand(args, usageDep, "unknown dep command %q", map[string]subcommandHandler{
		"add":    func(args []string) error { return a.runDepAdd(ctx, args) },
		"remove": func(args []string) error { return a.runDepRemove(ctx, args) },
		"list":   func(args []string) error { return a.runDepList(ctx, args) },
	})
}

func (a *App) runDepAdd(ctx context.Context, args []string) error {
	leading, remaining, err := a.parseLeadingArgs(args, usageDepAdd, 2)
	if err != nil {
		return err
	}

	blockedID, blockerID := leading[0], leading[1]
	fs := flag.NewFlagSet("dep add", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := addActorFlag(fs)

	err = a.parseFlags(fs, remaining, usageDepAdd)
	if err != nil {
		return err
	}

	err = requireNoExtraArgs(fs, usageDepAdd)
	if err != nil {
		return err
	}

	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

	actor, err := resolveActor(repoRoot, actorFlag)
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
	leading, remaining, err := a.parseLeadingArgs(args, usageDepRemove, 2)
	if err != nil {
		return err
	}

	blockedID, blockerID := leading[0], leading[1]
	fs := flag.NewFlagSet("dep remove", flag.ContinueOnError)
	actorFlag := addActorFlag(fs)

	err = a.parseFlags(fs, remaining, usageDepRemove)
	if err != nil {
		return err
	}

	err = requireNoExtraArgs(fs, usageDepRemove)
	if err != nil {
		return err
	}

	repoRoot, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

	actor, err := resolveActor(repoRoot, actorFlag)
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
	leading, remaining, err := a.parseLeadingArgs(args, usageDepList, 1)
	if err != nil {
		return err
	}

	id := leading[0]
	fs := flag.NewFlagSet("dep list", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	err = a.parseFlags(fs, remaining, usageDepList)
	if err != nil {
		return err
	}

	err = requireNoExtraArgs(fs, usageDepList)
	if err != nil {
		return err
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
	return a.runSubcommand(args, usageLabels, "unknown labels command %q", map[string]subcommandHandler{
		"add":    func(args []string) error { return a.runLabelsAdd(ctx, args) },
		"remove": func(args []string) error { return a.runLabelsRemove(ctx, args) },
		"list":   func(args []string) error { return a.runLabelsList(ctx, args) },
	})
}

func (a *App) runLabelsAdd(ctx context.Context, args []string) error {
	leading, remaining, err := a.parseLeadingArgs(args, usageLabelsAdd, 1)
	if err != nil {
		return err
	}

	id := leading[0]
	fs := flag.NewFlagSet("labels add", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := addActorFlag(fs)

	err = a.parseFlags(fs, remaining, usageLabelsAdd)
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

	actor, err := resolveActor(repoRoot, actorFlag)
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
	leading, remaining, err := a.parseLeadingArgs(args, usageLabelsRemove, 1)
	if err != nil {
		return err
	}

	id := leading[0]
	fs := flag.NewFlagSet("labels remove", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	actorFlag := addActorFlag(fs)

	err = a.parseFlags(fs, remaining, usageLabelsRemove)
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

	actor, err := resolveActor(repoRoot, actorFlag)
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
	leading, remaining, err := a.parseLeadingArgs(args, usageLabelsList, 1)
	if err != nil {
		return err
	}

	id := leading[0]
	fs := flag.NewFlagSet("labels list", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")

	err = a.parseFlags(fs, remaining, usageLabelsList)
	if err != nil {
		return err
	}

	err = requireNoExtraArgs(fs, usageLabelsList)
	if err != nil {
		return err
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
	jiraOut := fs.String("jira", "", "output Jira epic plan JSON for the given epic id")

	err := a.parseFlags(fs, args, "usage: tack export [--json] [--jira <epic-id>]")
	if err != nil {
		return err
	}

	if fs.NArg() != 0 {
		return errors.New("usage: tack export [--json] [--jira <epic-id>]")
	}

	jiraID := strings.TrimSpace(*jiraOut)
	if !*jsonOut && jiraID == "" {
		return errors.New("export requires --json or --jira")
	}

	_, s, err := openRepoStore()
	if err != nil {
		return err
	}
	defer closeStore(s)

	if jiraID != "" {
		data, err := s.ExportJira(ctx, jiraID)
		if err != nil {
			return err
		}

		return writeJSON(a.stdout, data)
	}

	data, err := s.Export(ctx)
	if err != nil {
		return err
	}

	return writeJSON(a.stdout, data)
}

func parseListFilter(name string, helpOutput io.Writer, args []string, allowAssignee bool) (store.ListFilter, bool, bool, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	status := fs.String("status", "", "status filter")
	label := fs.String("label", "", "label filter")
	kind := fs.String("type", "", "type filter")
	limit := fs.Int("limit", 0, "max results")
	assigneeValue := ""

	jsonOut := fs.Bool("json", false, "output JSON")
	summaryOut := fs.Bool("summary", false, "output compact JSON summaries")

	var assignee *string
	if allowAssignee {
		assignee = fs.String("assignee", "", "assignee filter")
	}

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

	if assignee != nil {
		assigneeValue = strings.TrimSpace(*assignee)
	}

	return store.ListFilter{
		Statuses:  singleValueFilter(strings.TrimSpace(*status)),
		Assignees: singleValueFilter(assigneeValue),
		Labels:    singleValueFilter(strings.TrimSpace(*label)),
		Types:     singleValueFilter(strings.TrimSpace(*kind)),
		Limit:     *limit,
	}, *jsonOut, *summaryOut, nil
}

func singleValueFilter(value string) []string {
	if value == "" {
		return nil
	}

	return []string{value}
}

func openRepoStore() (string, *store.Store, error) {
	return store.OpenRepo(".")
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

func printIssueDetail(w io.Writer, issue issues.IssueDetail) error {
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
		"blocked_by: " + formatLinkEndpointIDs(issue.BlockedBy, true),
		"blocks: " + formatLinkEndpointIDs(issue.Blocks, false),
		"description:",
		issue.Description,
		"comments:",
	}

	if len(issue.Comments) == 0 {
		lines = append(lines, "  (none)")
	} else {
		for _, comment := range issue.Comments {
			lines = append(lines, fmt.Sprintf("  [%d] %s %s", comment.ID, comment.Author, comment.CreatedAt.UTC().Format(time.RFC3339)))
			lines = appendIndentedBlock(lines, "    ", comment.Body)
		}
	}

	lines = append(lines, "events:")

	if len(issue.Events) == 0 {
		lines = append(lines, "  (none)")
	} else {
		for _, event := range issue.Events {
			lines = append(lines, fmt.Sprintf("  [%d] %s %s %s %s", event.ID, event.CreatedAt.UTC().Format(time.RFC3339), event.Actor, event.EventType, event.Payload))
		}
	}

	_, err := fmt.Fprintln(w, strings.Join(lines, "\n"))

	return err
}

func formatLinkEndpointIDs(links []issues.Link, source bool) string {
	if len(links) == 0 {
		return "(none)"
	}

	ids := make([]string, 0, len(links))
	for _, link := range links {
		if source {
			ids = append(ids, link.SourceID)
			continue
		}

		ids = append(ids, link.TargetID)
	}

	return strings.Join(ids, ",")
}

func appendIndentedBlock(lines []string, prefix, body string) []string {
	for line := range strings.SplitSeq(body, "\n") {
		lines = append(lines, prefix+line)
	}

	return lines
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

func printImportSummary(w io.Writer, input importFile, result store.ImportResult) error {
	_, err := fmt.Fprintf(w, "imported %d issues\n", len(result.CreatedIDs))
	if err != nil {
		return err
	}

	if input.kind == importFileKindSnapshot {
		_, err = fmt.Fprintf(
			w,
			"restored %d links, %d comments, %d events\n",
			len(input.snapshot.Links),
			len(input.snapshot.Comments),
			len(input.snapshot.Events),
		)
		if err != nil {
			return err
		}

		return nil
	}

	for _, issue := range input.manifest.Issues {
		alias := strings.TrimSpace(issue.ID)
		if alias == "" {
			continue
		}

		_, err = fmt.Fprintf(w, "%s\t%s\n", alias, result.AliasMap[alias])
		if err != nil {
			return err
		}
	}

	return nil
}

func (a *App) printUsage() error {
	_, err := fmt.Fprintln(a.stdout, `tack commands:
  tack init
  tack create
  tack import --file <path> [--json]
  tack show <id> [--json]
  tack tui
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
  tack export [--json] [--jira <epic-id>]`)

	return err
}

func (a *App) runHelp(ctx context.Context, args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		return a.printUsage()
	}

	handler, ok := a.commandHandlers(ctx)[args[0]]
	if !ok {
		return fmt.Errorf("unknown command %q", args[0])
	}

	withHelp := append(args[1:], "--help")

	return handler(withHelp)
}

func (a *App) parseFlags(fs *flag.FlagSet, args []string, usage string) error {
	return parseFlagSet(fs, a.stdout, args, usage)
}

func parseFlagSet(fs *flag.FlagSet, helpOutput io.Writer, args []string, usage string) error {
	return parseFlagSetWithHelp(fs, helpOutput, args, func(out io.Writer, flags *flag.FlagSet) error {
		return printFlagUsage(out, usage, flags)
	})
}

func parseFlagSetWithHelp(fs *flag.FlagSet, helpOutput io.Writer, args []string, onHelp func(io.Writer, *flag.FlagSet) error) error {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	err := fs.Parse(args)
	if err == nil {
		return nil
	}

	if errors.Is(err, flag.ErrHelp) {
		usageErr := onHelp(helpOutput, fs)
		if usageErr != nil {
			return usageErr
		}

		return flag.ErrHelp
	}

	return err
}

func printFlagUsage(w io.Writer, usage string, fs *flag.FlagSet) error {
	return printUsageWithDefaults(w, usage, func(out io.Writer) error {
		return printFlagDefaults(out, fs)
	})
}

func (a *App) printCommandUsage(w io.Writer, usage string, defaults func(io.Writer) error) error {
	return printUsageWithDefaults(w, usage, defaults)
}

func (a *App) parseLeadingArgs(args []string, usage string, required int) ([]string, []string, error) {
	if len(args) > 0 && isHelpToken(args[0]) {
		err := a.printCommandUsage(a.stdout, usage, nil)
		if err != nil {
			return nil, nil, err
		}

		return nil, nil, flag.ErrHelp
	}

	if len(args) < required {
		return nil, nil, errors.New(usage)
	}

	leading := make([]string, required)
	copy(leading, args[:required])

	return leading, args[required:], nil
}

func (a *App) runSubcommand(args []string, usage, unknownFormat string, handlers map[string]subcommandHandler) error {
	if len(args) == 0 {
		return errors.New(usage)
	}

	if isHelpToken(args[0]) {
		return a.printCommandUsage(a.stdout, usage, nil)
	}

	handler, ok := handlers[args[0]]
	if !ok {
		return fmt.Errorf(unknownFormat, args[0])
	}

	return handler(args[1:])
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

func parseTUIFlagSet(fs *flag.FlagSet, helpOutput io.Writer, args []string) error {
	return parseFlagSetWithHelp(fs, helpOutput, args, printTUIUsage)
}

func parseUpdateFlagSet(fs *flag.FlagSet, helpOutput io.Writer, args []string) error {
	return parseFlagSetWithHelp(fs, helpOutput, args, printUpdateUsage)
}

func printUpdateUsage(w io.Writer, fs *flag.FlagSet) error {
	return printUsageWithDefaults(w, usageUpdate, func(out io.Writer) error {
		_, err := fmt.Fprintln(out, `Update one or more mutable fields on an existing issue. Only flags you pass are changed.

examples:
  tack update tk-42 --claim
  tack update tk-42 --status in_progress --priority high
  tack update tk-42 --description "Clarified scope and acceptance criteria"
  tack update tk-42 --body-file /tmp/issue.md
  tack update tk-42 --assignee ""
  tack update tk-42 --parent ""

notes:
  at least one change flag is required
  --description and --body-file both set the description; use only one of them
  --assignee "" clears the assignee
  --parent "" clears the parent
  --claim assigns the issue to the resolved actor and moves open issues to in_progress
  valid --type values: epic, task, bug, feature
  valid --status values: open, in_progress, blocked, closed
  valid --priority values: low, medium, high, urgent
  use tack close/tack reopen when you want to record a reason for status changes
  use tack labels add|remove and tack dep add|remove for labels and dependencies`)
		if err != nil {
			return err
		}

		return printFlagDefaults(out, fs)
	})
}

func printTUIUsage(w io.Writer, fs *flag.FlagSet) error {
	return printUsageWithDefaults(w, usageTUI, func(out io.Writer) error {
		_, err := fmt.Fprintln(out, `keyboard controls:
  q            quit
  ?            toggle help
  tab          switch panes or tabs
  shift+tab    switch panes or tabs in reverse
  j/k, arrows  move selection
  /            open guided filter picker
  r            toggle list and ready views
  enter        pin or open the selected issue
  g            open the graph tab
  G            open the graph tab
  esc          close the picker or return focus
  auto-refresh refreshes from disk every 5 seconds
  ctrl+r       refresh data from disk`)
		if err != nil {
			return err
		}

		return printFlagDefaults(out, fs)
	})
}

func printFlagDefaults(out io.Writer, fs *flag.FlagSet) error {
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
}

func readImportFile(path string) (_ importFile, err error) {
	var input importFile

	file, err := os.Open(path)
	if err != nil {
		return input, err
	}

	defer func() {
		closeErr := file.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	data, err := io.ReadAll(file)
	if err != nil {
		return input, err
	}

	kind, err := detectImportFileKind(data)
	if err != nil {
		return input, err
	}

	switch kind {
	case importFileKindSnapshot:
		err = decodeStrictJSON(data, &input.snapshot)
	default:
		err = decodeStrictJSON(data, &input.manifest)
	}

	if err != nil {
		return importFile{}, err
	}

	input.kind = kind

	return input, nil
}

func detectImportFileKind(data []byte) (importFileKind, error) {
	var raw map[string]json.RawMessage

	err := decodeStrictJSON(data, &raw)
	if err != nil {
		return "", err
	}

	for _, key := range []string{"metadata", "issue_data", "links", "comments", "events"} {
		if _, ok := raw[key]; ok {
			return importFileKindSnapshot, nil
		}
	}

	issuesRaw, ok := raw["issues"]
	if !ok {
		return importFileKindManifest, nil
	}

	var issueEntries []map[string]json.RawMessage

	err = json.Unmarshal(issuesRaw, &issueEntries)
	if err != nil {
		return importFileKindManifest, nil
	}

	for _, issue := range issueEntries {
		for _, key := range []string{"sequence", "status", "assignee", "created_at", "updated_at", "closed_at", "parent_id"} {
			if _, ok := issue[key]; ok {
				return importFileKindSnapshot, nil
			}
		}
	}

	return importFileKindManifest, nil
}

func decodeStrictJSON(data []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	err := dec.Decode(target)
	if err != nil {
		return err
	}

	err = dec.Decode(&struct{}{})
	if !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("manifest must contain a single JSON value")
		}

		return err
	}

	return nil
}

func isHelpToken(arg string) bool {
	return arg == "-h" || arg == "--help"
}

func addActorFlag(fs *flag.FlagSet) *string {
	return fs.String("actor", "", "actor override")
}

func resolveActor(repoRoot string, actorFlag *string) (string, error) {
	if actorFlag == nil {
		return issues.ResolveActor(repoRoot, "")
	}

	return issues.ResolveActor(repoRoot, *actorFlag)
}

func requireNoExtraArgs(fs *flag.FlagSet, usage string) error {
	if fs.NArg() != 0 {
		return errors.New(usage)
	}

	return nil
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
