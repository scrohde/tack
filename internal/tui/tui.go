package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"tack/internal/store"
)

type DataSource string

const (
	DataSourceAll   DataSource = "all"
	DataSourceReady DataSource = "ready"
)

type StartupOptions struct {
	Source DataSource
	Filter store.ListFilter
}

func Run(ctx context.Context, stdout, _ io.Writer, options StartupOptions) error {
	repoRoot, s, err := store.OpenRepo(".")
	if err != nil {
		return err
	}
	defer closeStore(s)

	_ = ctx

	if stdout == nil {
		return nil
	}

	_, err = fmt.Fprintf(
		stdout,
		"tack tui bootstrap\nrepo: %s\nsource: %s\nfilters: %s\n",
		repoRoot,
		options.DataSource(),
		formatFilter(options.Filter),
	)

	return err
}

func (o StartupOptions) DataSource() DataSource {
	if o.Source == "" {
		return DataSourceAll
	}

	return o.Source
}

func formatFilter(filter store.ListFilter) string {
	parts := []string{}

	if filter.Status != "" {
		parts = append(parts, "status="+filter.Status)
	}

	if filter.Type != "" {
		parts = append(parts, "type="+filter.Type)
	}

	if filter.Label != "" {
		parts = append(parts, "label="+filter.Label)
	}

	if filter.Assignee != "" {
		parts = append(parts, "assignee="+filter.Assignee)
	}

	if filter.Limit > 0 {
		parts = append(parts, fmt.Sprintf("limit=%d", filter.Limit))
	}

	if len(parts) == 0 {
		return "(none)"
	}

	return strings.Join(parts, " ")
}

func closeStore(s *store.Store) {
	err := s.Close()
	if err != nil {
		return
	}
}
