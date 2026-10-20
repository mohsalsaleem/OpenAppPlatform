package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"
	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1/oapv1connect"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
	"google.golang.org/protobuf/proto"
)

// creds is an agent's saved session: ~/.config/oap/agents/NAME/credentials.json.
type creds struct {
	URL          string   `json:"url"`
	Agent        string   `json:"agent"`
	Token        string   `json:"token"`
	ExpiresAt    string   `json:"expires_at"`
	Applications []string `json:"applications,omitempty"`
	May          []string `json:"may,omitempty"`
}

func (a *app) agentDir(name string) string { return filepath.Join(a.dir(), "agents", name) }

// identityKey loads or creates the agent's Ed25519 key (0600).
func (a *app) identityKey(name string) (ed25519.PrivateKey, string, error) {
	path := filepath.Join(a.agentDir(name), "identity")
	if b, err := os.ReadFile(path); err == nil {
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, "", fmt.Errorf("the identity key %s is damaged", path)
		}
		priv := ed25519.NewKeyFromSeed(seed)
		return priv, "ed25519:" + base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(a.agentDir(name), 0o700); err != nil {
		return nil, "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(priv.Seed()) + "\n"); err != nil {
		return nil, "", err
	}
	return priv, "ed25519:" + base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)), nil
}

func (a *app) saveCreds(c creds) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.agentDir(c.Agent), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(a.agentDir(c.Agent), ".credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(a.agentDir(c.Agent), "credentials.json"))
}

// credentials returns an agent's session, signing in again when the token
// has expired: a trusted identity is approved at once.
func (a *app) credentials(ctx context.Context, name string) (creds, error) {
	var c creds
	b, err := os.ReadFile(filepath.Join(a.agentDir(name), "credentials.json"))
	if err != nil {
		return c, fmt.Errorf("agent %s isn't signed in; run %s login URL --agent %s", name, a.cli, name)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if exp, err := time.Parse(time.RFC3339, c.ExpiresAt); err == nil && a.now().Add(time.Minute).Before(exp) {
		return c, nil
	}
	fresh, err := a.signIn(ctx, c.URL, name, false)
	if err != nil {
		return c, fmt.Errorf("agent %s's token expired and signing in again failed: %v", name, err)
	}
	return fresh, nil
}

// signIn runs a device login. With wait, a login that needs the owner
// shows its code and waits; without, it fails instead.
func (a *app) signIn(ctx context.Context, url, name string, wait bool) (creds, error) {
	priv, pub, err := a.identityKey(name)
	if err != nil {
		return creds{}, err
	}
	signedAt := a.now().UTC().Format(time.RFC3339)
	nonce := make([]byte, 18)
	rand.Read(nonce)
	n := base64.RawURLEncoding.EncodeToString(nonce)
	req := &oapv1.LoginRequest{Agent: name, PublicKey: pub, SignedAt: signedAt, Nonce: n,
		Signature: ed25519.Sign(priv, service.LoginPayload(name, pub, signedAt, n))}
	client := oapv1connect.NewAgentServiceClient(http.DefaultClient, url)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.Login(cctx, connect.NewRequest(req))
	if err != nil {
		return creds{}, err
	}
	defer stream.Close()
	for stream.Receive() {
		switch st := stream.Msg().State.(type) {
		case *oapv1.LoginResponse_Pending:
			if !wait {
				return creds{}, errors.New("the owner must approve this login; run " + a.cli + " login")
			}
			p := st.Pending
			a.u().Note(fmt.Sprintf("Waiting for the owner. Code: %s\nThe owner approves with: %s owner approve %s --app APP --may scale,deploy\n(expires in %s)",
				p.UserCode, a.cli, p.UserCode, time.Duration(p.ExpiresInSeconds)*time.Second))
			if a.mode != human {
				a.printJSON(stream.Msg())
			}
		case *oapv1.LoginResponse_Approved:
			ap := st.Approved
			c := creds{URL: url, Agent: name, Token: ap.Token, ExpiresAt: a.now().Add(time.Duration(ap.ExpiresInSeconds) * time.Second).UTC().Format(time.RFC3339),
				Applications: ap.GetScope().GetApplications(), May: ap.GetScope().GetMay()}
			return c, a.saveCreds(c)
		case *oapv1.LoginResponse_Refused:
			return creds{}, fmt.Errorf("%s: %s", st.Refused.Why, st.Refused.Message)
		}
	}
	if err := stream.Err(); err != nil {
		return creds{}, err
	}
	return creds{}, errors.New("the login ended without an answer")
}

// loginCmd: login URL --agent NAME.
func (a *app) loginCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	agent := fs.String("agent", os.Getenv("OAP_AGENT"), "the name to sign in as, for example claude-laptop")
	asJSON := fs.Bool("json", false, "")
	url, rest := splitFirst(args)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if url == "" || *agent == "" {
		return a.fail(errors.New("usage: login URL --agent NAME"))
	}
	if !strings.Contains(url, "://") {
		url = "https://" + url
	}
	if *asJSON {
		a.mode = jsonMode
		defer func() { a.mode = human }()
	}
	c, err := a.signIn(ctx, url, *agent, true)
	if err != nil {
		return a.fail(err)
	}
	if *asJSON {
		out, _ := json.Marshal(map[string]any{"agent": c.Agent, "url": c.URL, "expires_at": c.ExpiresAt, "applications": c.Applications, "may": c.May})
		fmt.Fprintf(a.out, "%s\n", out)
		return 0
	}
	a.u().Success(fmt.Sprintf("Signed in to %s as %s until %s; it may %s on %s. Use it with OAP_AGENT=%s.",
		c.URL, c.Agent, c.ExpiresAt, strings.Join(c.May, ", "), strings.Join(c.Applications, ", "), c.Agent))
	return 0
}

// ownerCmd: owner logins|approve|deny|agents|revoke|pause|resume|mode.
func (a *app) ownerCmd(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return a.fail(errors.New("usage: owner logins|approve CODE --app APP [--may ACTIONS] [--trust=false]|deny CODE|agents|revoke AGENT [--forget]|pause [AGENT]|resume [AGENT]|mode trusted|every-session"))
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("owner "+sub, flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	apps := fs.String("app", "", "applications the agent may act on, comma-separated, or *")
	may := fs.String("may", "scale,deploy,rollback,configure", "intent kinds the agent may propose, comma-separated")
	trust := fs.Bool("trust", true, "trust the agent's key, so later sessions sign in without asking")
	forget := fs.Bool("forget", false, "also forget the agent's trusted identity")
	asJSON := fs.Bool("json", false, "")
	arg, rest := splitFirst(rest)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	s, err := a.serverFor("")
	if err != nil {
		return a.fail(err)
	}
	hc, base, stop, err := a.transport(ctx, s)
	if err != nil {
		return a.fail(err)
	}
	defer stop()
	c := oapv1connect.NewOwnerServiceClient(hc, base)
	var out proto.Message
	switch sub {
	case "logins":
		res, e := c.ListLogins(ctx, connect.NewRequest(&oapv1.ListLoginsRequest{}))
		err = e
		if e == nil {
			out = res.Msg
			if !*asJSON {
				if len(res.Msg.Logins) == 0 {
					fmt.Fprintln(a.out, "No logins are waiting.")
				}
				for _, l := range res.Msg.Logins {
					fmt.Fprintf(a.out, "%s  %s  key %s\n", l.UserCode, l.Agent, or(l.KeyFingerprint, "none"))
				}
				return 0
			}
		}
	case "approve":
		if *apps == "" {
			return a.fail(errors.New("name the applications with --app (comma-separated, or *)"))
		}
		res, e := c.ApproveLogin(ctx, connect.NewRequest(&oapv1.ApproveLoginRequest{UserCode: arg, Applications: split(*apps), May: split(*may), Trust: *trust}))
		err = e
		if e == nil {
			out = res.Msg
			if !*asJSON {
				ag := res.Msg.Agent
				fmt.Fprintf(a.out, "✓ Approved %s for %s; it may %s. Trusted identity: %t.\n", ag.Name, strings.Join(ag.Applications, ", "), strings.Join(ag.May, ", "), ag.Trusted)
				return 0
			}
		}
	case "deny":
		_, err = c.DenyLogin(ctx, connect.NewRequest(&oapv1.DenyLoginRequest{UserCode: arg}))
	case "agents":
		res, e := c.ListAgents(ctx, connect.NewRequest(&oapv1.ListAgentsRequest{}))
		err = e
		if e == nil {
			out = res.Msg
			if !*asJSON {
				fmt.Fprintf(a.out, "approval mode: %s%s\n", strings.ToLower(strings.TrimPrefix(res.Msg.Mode.String(), "APPROVAL_MODE_")), map[bool]string{true: " · ALL PAUSED"}[res.Msg.AllPaused])
				for _, ag := range res.Msg.Agents {
					state := "active"
					if ag.Paused {
						state = "paused"
					}
					fmt.Fprintf(a.out, "%-20s %-7s apps %-20s may %-30s trusted %-5t tokens %d\n", ag.Name, state, strings.Join(ag.Applications, ","), strings.Join(ag.May, ","), ag.Trusted, ag.ActiveTokens)
				}
				return 0
			}
		}
	case "revoke":
		res, e := c.Revoke(ctx, connect.NewRequest(&oapv1.RevokeRequest{Agent: arg, ForgetIdentity: *forget}))
		err = e
		if e == nil {
			out = res.Msg
			if !*asJSON {
				fmt.Fprintf(a.out, "✓ Revoked %d tokens of %s.\n", res.Msg.RevokedTokens, arg)
				return 0
			}
		}
	case "pause":
		_, err = c.Pause(ctx, connect.NewRequest(&oapv1.PauseRequest{Agent: arg}))
	case "resume":
		_, err = c.Resume(ctx, connect.NewRequest(&oapv1.ResumeRequest{Agent: arg}))
	case "asks":
		res, e := c.ListAsks(ctx, connect.NewRequest(&oapv1.ListAsksRequest{}))
		err = e
		if e == nil {
			out = res.Msg
			if !*asJSON {
				if len(res.Msg.Asks) == 0 {
					fmt.Fprintln(a.out, "No asks are waiting.")
				}
				for _, q := range res.Msg.Asks {
					kind := strings.ToLower(strings.TrimPrefix(q.Request.GetKind().String(), "ASK_KIND_"))
					line := fmt.Sprintf("%s  %-8s %s asks: %s", q.AskId, kind, q.Agent, q.Request.GetQuestion())
					if o := q.Request.GetOptions(); len(o) > 0 {
						line += "  [" + strings.Join(o, " | ") + "]"
					}
					if p := q.Request.GetProposalId(); p != "" {
						line += "  proposal " + p
					}
					fmt.Fprintf(a.out, "%s  (expires %s)\n", line, q.ExpiresAt.AsTime().UTC().Format("15:04 UTC"))
				}
				return 0
			}
		}
	case "answer":
		answer := strings.Join(fs.Args(), " ")
		if arg == "" || answer == "" {
			return a.fail(errors.New("usage: owner answer ASK_ID ANSWER"))
		}
		_, err = c.AnswerAsk(ctx, connect.NewRequest(&oapv1.AnswerAskRequest{AskId: arg, Answer: answer}))
		if err == nil {
			fmt.Fprintf(a.out, "✓ Answered %s: %s\n", arg, answer)
			return 0
		}
	case "mode":
		m := map[string]oapv1.ApprovalMode{"trusted": oapv1.ApprovalMode_APPROVAL_MODE_TRUSTED_IDENTITY, "every-session": oapv1.ApprovalMode_APPROVAL_MODE_EVERY_SESSION}[arg]
		if m == 0 {
			return a.fail(errors.New("mode is trusted or every-session"))
		}
		_, err = c.SetApprovalMode(ctx, connect.NewRequest(&oapv1.SetApprovalModeRequest{Mode: m}))
	default:
		return a.fail(fmt.Errorf("unknown owner command %q", sub))
	}
	if err != nil {
		return a.fail(err)
	}
	switch {
	case out != nil && *asJSON:
		a.printJSON(out)
	default:
		target := arg
		if target == "" {
			target = "every agent"
		}
		fmt.Fprintf(a.out, "✓ %s %s\n", map[string]string{"deny": "Denied", "pause": "Paused", "resume": "Resumed", "mode": "Approval mode set to"}[sub], target)
	}
	return 0
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
