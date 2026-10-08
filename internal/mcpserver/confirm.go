package mcpserver

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	approvalInput = "approval"
	approvalTTL   = 10 * time.Minute
)

var (
	errNoElicitation = errors.New("this change needs your approval, but the MCP client cannot show approval dialogs (elicitation); nothing was changed")
	errDeclined      = errors.New("the change was not approved; nothing was changed")
	errStale         = errors.New("the approval does not match this change, was already used or has expired; nothing was changed")
)

var approvalSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"approve": map[string]any{
			"type":        "boolean",
			"title":       "Approve this change",
			"description": "Leave unchecked or cancel to reject.",
		},
	},
	"required": []string{"approve"},
}

// gate asks the human to approve a write through an elicitation, which
// the client shows to the user and the model cannot answer.
//
// A write tool first returns an input request carrying the plan text. The
// client (or the SDK, for older protocol versions) collects the answer and
// calls the tool again with it. The request state is a one-time nonce plus
// an HMAC over the tool, its arguments and the exact plan text shown, so
// an approval cannot be replayed, forged or applied to a plan that changed
// in the meantime.
type gate struct {
	key     []byte
	mu      sync.Mutex
	pending map[string]time.Time
	now     func() time.Time
}

func newGate() *gate {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return &gate{key: key, pending: map[string]time.Time{}, now: time.Now}
}

// check returns (nil, nil) when the user approved exactly this plan. It
// returns an input-required result to hand back to the client, or an error.
func (g *gate) check(req *mcp.CallToolRequest, tool string, args any, plan string) (*mcp.CallToolResult, error) {
	caps := req.ClientCapabilities()
	if caps == nil || caps.Elicitation == nil || (caps.Elicitation.Form == nil && caps.Elicitation.URL != nil) {
		return nil, errNoElicitation
	}
	argJSON, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(tool + "\x00" + string(argJSON) + "\x00" + plan))
	d := hex.EncodeToString(digest[:])

	if resp, ok := req.Params.InputResponses[approvalInput]; ok {
		if !g.redeem(req.Params.RequestState, d) {
			return nil, errStale
		}
		res, ok := resp.(*mcp.ElicitResult)
		if !ok || res.Action != "accept" || res.Content["approve"] != true {
			return nil, errDeclined
		}
		return nil, nil
	}
	return &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{
			approvalInput: &mcp.ElicitParams{Message: plan, RequestedSchema: approvalSchema},
		},
		RequestState: g.issue(d),
	}, nil
}

func (g *gate) sign(nonce, digest string) string {
	m := hmac.New(sha256.New, g.key)
	m.Write([]byte(nonce + "\x00" + digest))
	return hex.EncodeToString(m.Sum(nil))
}

func (g *gate) issue(digest string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	nonce := hex.EncodeToString(b)
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for n, t := range g.pending {
		if now.Sub(t) > approvalTTL {
			delete(g.pending, n)
		}
	}
	g.pending[nonce] = now
	return nonce + "." + g.sign(nonce, digest)
}

func (g *gate) redeem(state, digest string) bool {
	nonce, sig, ok := strings.Cut(state, ".")
	if !ok {
		return false
	}
	g.mu.Lock()
	issued, found := g.pending[nonce]
	delete(g.pending, nonce)
	g.mu.Unlock()
	if !found || g.now().Sub(issued) > approvalTTL {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(g.sign(nonce, digest)))
}
