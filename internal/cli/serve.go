package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/accounts"
	"github.com/samahlstrom/retake/internal/app"
	"github.com/samahlstrom/retake/internal/blob"
	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/hub"
	"github.com/samahlstrom/retake/internal/probe"
)

// retake serve and its admin commands (spec/server.md).

// serverFlags are the settings every server command takes as flags.
type serverFlags struct {
	data, listen, publicURL, database string
	s3Endpoint, s3Bucket, s3Region    string
	smtpFrom, uiDir                   string
	proxies                           []string
	s3Insecure, verbose               bool
}

// add puts the server flags into f.
func (sf *serverFlags) add(f *flags) {
	if f.values == nil {
		f.values = map[string]*string{}
	}
	if f.bools == nil {
		f.bools = map[string]*bool{}
	}
	if f.multi == nil {
		f.multi = map[string]*[]string{}
	}
	for name, p := range map[string]*string{"--data": &sf.data, "--listen": &sf.listen, "--public-url": &sf.publicURL,
		"--database": &sf.database, "--s3-endpoint": &sf.s3Endpoint, "--s3-bucket": &sf.s3Bucket,
		"--s3-region": &sf.s3Region, "--smtp-from": &sf.smtpFrom} {
		f.values[name] = p
	}
	f.bools["--s3-insecure"] = &sf.s3Insecure
	f.multi["--trust-proxy"] = &sf.proxies
}

// serverSettings are a server command's effective settings.
type serverSettings struct {
	Data, Listen, PublicURL, Database string
	Proxies                           []string
	S3                                *blob.S3Config
	SMTPURL, SMTPFrom                 string
}

// resolveServer combines flags, RETAKE_* environment variables, config.toml's [server] table and the defaults.
func resolveServer(env Env, cfg config.Config, sf serverFlags) (serverSettings, error) {
	c := cfg.Server
	pick := func(flag, envName string, file *string, def string) string {
		if flag != "" {
			return flag
		}
		if v := strings.TrimSpace(env.Getenv(envName)); v != "" {
			return v
		}
		if file != nil && strings.TrimSpace(*file) != "" {
			return strings.TrimSpace(*file)
		}
		return def
	}
	s := serverSettings{
		Data:      pick(sf.data, "RETAKE_DATA", c.Data, ""),
		Listen:    pick(sf.listen, "RETAKE_LISTEN", c.Listen, ":8080"),
		PublicURL: pick(sf.publicURL, "RETAKE_PUBLIC_URL", c.PublicURL, ""),
		Database:  pick(sf.database, "RETAKE_DATABASE_URL", c.Database, ""),
		SMTPURL:   pick("", "RETAKE_SMTP_URL", c.SMTPURL, ""),
		SMTPFrom:  pick(sf.smtpFrom, "RETAKE_SMTP_FROM", c.SMTPFrom, ""),
	}
	switch {
	case len(sf.proxies) > 0:
		s.Proxies = sf.proxies
	case env.Getenv("RETAKE_TRUSTED_PROXIES") != "":
		s.Proxies = []string{env.Getenv("RETAKE_TRUSTED_PROXIES")}
	default:
		s.Proxies = c.TrustedProxies
	}
	if s.Data == "" {
		return s, errors.New("no data folder: give --data DIR, or set RETAKE_DATA")
	}
	s.Data = absPath(s.Data, env.Getenv)
	if _, err := strconv.Atoi(s.Listen); err == nil {
		s.Listen = ":" + s.Listen // a bare port
	}
	if endpoint := pick(sf.s3Endpoint, "RETAKE_S3_ENDPOINT", c.S3Endpoint, ""); endpoint != "" {
		insecure := sf.s3Insecure || truthy(env.Getenv("RETAKE_S3_INSECURE")) || c.S3Insecure != nil && *c.S3Insecure
		s.S3 = &blob.S3Config{Endpoint: endpoint, Bucket: pick(sf.s3Bucket, "RETAKE_S3_BUCKET", c.S3Bucket, "retake"),
			AccessKey: pick("", "RETAKE_S3_ACCESS_KEY", c.S3AccessKey, ""),
			SecretKey: pick("", "RETAKE_S3_SECRET_KEY", c.S3SecretKey, ""),
			Region:    pick(sf.s3Region, "RETAKE_S3_REGION", c.S3Region, blob.DefaultRegion), Insecure: insecure}
		if s.S3.AccessKey == "" || s.S3.SecretKey == "" {
			return s, errors.New("S3 needs its keys: set RETAKE_S3_ACCESS_KEY and RETAKE_S3_SECRET_KEY")
		}
	}
	return s, nil
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// openStores opens a server command's stores. Admin commands leave object storage out: they touch only the
// database and the workspaces, and a running server uploads what they add.
func openStores(ctx context.Context, s serverSettings, withMedia bool) (*hub.Stores, error) {
	cfg := hub.Config{Data: s.Data, Database: s.Database}
	if withMedia {
		cfg.S3 = s.S3
	}
	return hub.OpenStores(ctx, cfg)
}

// mailer returns the configured mailer, or nil.
func (s serverSettings) mailer() (*hub.SMTP, error) {
	if s.SMTPURL == "" {
		return nil, nil //nolint:nilnil // no mail is not an error
	}
	pub, _ := hub.ParsePublicURL(s.PublicURL)
	return hub.ParseSMTP(s.SMTPURL, s.SMTPFrom, pub.Host)
}

// linkBase is the origin the CLI makes links for: the public URL, else the one the server last ran with, else
// http://localhost:<listen port>.
func linkBase(s serverSettings, st *hub.Stores) string {
	if s.PublicURL != "" {
		if p, err := hub.ParsePublicURL(s.PublicURL); err == nil {
			return p.Origin
		}
	}
	if v, err := st.Accounts.Setting(accounts.SettingPublicURL); err == nil && v != "" {
		return v
	}
	port := "8080"
	if _, p, err := net.SplitHostPort(s.Listen); err == nil && p != "" {
		port = p
	}
	return "http://localhost:" + port
}

func serveCmd(ctx context.Context, args []string, env Env) int {
	var sf serverFlags
	f := flags{values: map[string]*string{"--ui-dir": &sf.uiDir}, bools: map[string]*bool{"--verbose": &sf.verbose}}
	sf.add(&f)
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err == nil && len(pos) > 0 {
		err = fmt.Errorf("unexpected argument %q", pos[0])
	}
	cfg := loadConfig(env)
	var s serverSettings
	if err == nil {
		s, err = resolveServer(env, cfg, sf)
	}
	if err != nil {
		return usageError(env, "serve", err)
	}
	pub, err := hub.ParsePublicURL(s.PublicURL)
	if err != nil {
		return usageError(env, "serve", err)
	}
	if _, err := hub.ParseProxies(s.Proxies); err != nil {
		return usageError(env, "serve", err)
	}
	mail, err := s.mailer()
	if err != nil {
		return usageError(env, "serve", err)
	}
	page, ok := pageFiles(env, sf.uiDir)
	if !ok {
		return ExitUsage
	}
	st, err := openStores(ctx, s, true)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake serve: %v\n", err)
		return ExitError
	}
	defer func() { _ = st.Close() }()

	prober := env.Prober
	if prober == nil {
		if !probe.Available() {
			_, _ = fmt.Fprintln(env.Stderr, "retake: ffprobe not found on PATH: install ffmpeg so versions can be detected")
		}
		prober = probe.New(env.Stderr)
	}
	engines := app.Engines(cfg)
	if env.Engines != nil {
		engines = env.Engines(cfg)
	}
	o := hub.Options{Stores: st, PublicURL: s.PublicURL, Proxies: s.Proxies, UI: page, Jobs: &engines, Prober: prober,
		Version: Version(), Log: env.Stderr, Verbose: sf.verbose}
	if mail != nil {
		o.Mailer = mail
	}
	h, err := hub.New(o)
	if err != nil {
		return usageError(env, "serve", err)
	}
	ln, err := net.Listen("tcp", s.Listen)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake serve: listening on %s: %v\n", s.Listen, err)
		return ExitUsage
	}
	base := pub.Origin
	if base == "" {
		base = "http://localhost:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	}
	printServeStartup(env, st, s, pub, base)
	h.Start()
	if env.Ready != nil {
		env.Ready(base + "/")
	}
	serr := h.Serve(ctx, ln)
	if cerr := h.Close(); cerr != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake: stopping: %v\n", cerr)
	}
	if serr != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake serve: %v\n", serr)
		return ExitError
	}
	_, _ = fmt.Fprintln(env.Stdout, "\nretake: stopped")
	return ExitOK
}

func printServeStartup(env Env, st *hub.Stores, s serverSettings, pub hub.PublicURL, base string) {
	ps, _ := st.Accounts.Projects()
	users, _ := st.Accounts.CountUsers()
	w := env.Stdout
	_, _ = fmt.Fprintf(w, "retake serve: %s, %s (%s)\n", plural(len(ps), "project"), plural(users, "user"),
		st.Describe())
	_, _ = fmt.Fprintf(w, "  data   %s\n", s.Data)
	_, _ = fmt.Fprintf(w, "  url    %s/   (listening on %s)\n", base, s.Listen)
	if users == 0 {
		_, _ = fmt.Fprintf(w, "  no users yet: add the first admin with\n    retake user add you@example.com --admin "+
			"--data %s\n", s.Data)
	}
	_, _ = fmt.Fprintln(w, "  Ctrl+C stops the server")
	switch {
	case pub.Origin == "":
		_, _ = fmt.Fprintln(env.Stderr, "retake: no --public-url: any Host is accepted and links are made for "+
			"localhost. Set it to the address people use.")
	case pub.Scheme == "http" && !pub.Loopback():
		_, _ = fmt.Fprintln(env.Stderr, "retake: the public URL is plain http, so cookies aren't Secure and can be "+
			"read on the network. Put retake behind TLS (deploy/README.md).")
	}
}
