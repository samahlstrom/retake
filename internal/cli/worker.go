package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/samahlstrom/retake/internal/worker"
)

func workerCmd(ctx context.Context, args []string, env Env) int {
	var remote, root, name, profiles string
	f := flags{values: map[string]*string{"--remote": &remote, "--project": &root, "--name": &name, "--profiles": &profiles}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err != nil {
		return usageError(env, "worker", err)
	}
	if len(pos) != 0 || remote == "" || root == "" {
		return usageError(env, "worker", errors.New("use --remote NAME --project DIR [--name NAME] [--profiles FILE]"))
	}
	rs, _, err := loadRemotes(env.Getenv)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	r, ok := rs[remote]
	if !ok {
		return usageError(env, "worker", fmt.Errorf("remote %q does not exist; use retake remote add", remote))
	}
	ps := worker.DefaultProfiles()
	if profiles != "" {
		b, e := os.ReadFile(absPath(profiles, env.Getenv))
		if e != nil {
			err = e
		} else {
			err = json.Unmarshal(b, &ps)
		}
		if err != nil {
			return usageError(env, "worker", err)
		}
	}
	err = worker.Run(ctx, worker.Options{URL: r.URL, Token: r.Token, Root: absPath(root, env.Getenv), Name: name, Profiles: ps, Editor: editorOf(env), Log: env.Stderr})
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake worker: %v\n", err)
		return ExitError
	}
	return ExitOK
}
