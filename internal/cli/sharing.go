package cli

import (
	"os"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/sharing"
)

// videoSharing is the address of links made in the page of retake open <video> (spec/team.md, "How a link reaches
// people"): a running share of the video, an --allow-host name, or a Cloudflare quick tunnel started for it. Its
// hosts are the video's store's. It has no local-network listener (retake open <folder> has).
func videoSharing(env Env, cfg config.Config, video string, port int, allow []string) *sharing.Local {
	root := project.Root(video)
	withStore := func(create bool, f func(*project.Store)) {
		var st *project.Store
		var err error
		if create {
			st, err = project.Open(root)
		} else {
			st, err = project.OpenExisting(root)
		}
		if err != nil {
			return
		}
		defer func() { _ = st.Close() }()
		f(st)
	}
	o := sharing.Options{Port: port, Allow: allow, Off: cfg.Share.TunnelOff(), Log: env.Stderr,
		Hosts: func() (hosts []project.Host) {
			withStore(false, func(st *project.Store) { hosts, _ = st.Hosts() })
			return hosts
		},
		AddHost: func(host string) (err error) {
			withStore(true, func(st *project.Store) { err = st.AddHost(host, sharing.KindTunnel, os.Getpid()) })
			return err
		},
		RemoveHost: func(host string) {
			withStore(false, func(st *project.Store) { _ = st.RemoveHost(host, os.Getpid()) })
		},
	}
	if p := cfg.Share.Cloudflared; p != nil && *p != "" {
		o.Cloudflared = absPath(*p, env.Getenv)
	}
	return sharing.New(o)
}
