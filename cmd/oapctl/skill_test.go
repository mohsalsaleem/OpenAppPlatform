package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/desiredyaml"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/skills"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/agentapi"
)

// The guide and schema as they are in the repository.
func repoFiles(t *testing.T) (guide, schema string) {
	t.Helper()
	g, err := os.ReadFile("../../internal/core/skills/author-app.md")
	if err != nil {
		t.Fatal(err)
	}
	s, err := os.ReadFile("../../internal/core/spec/application.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(g), string(s)
}

// TestSkillServedMatchesRepository: oapd serves the author-app skill and
// the oap.yaml schema exactly as they are in the repository, as an MCP
// prompt and resources, over HTTPS and through oapctl, to signed-in
// callers only.
func TestSkillServedMatchesRepository(t *testing.T) {
	guide, schema := repoFiles(t)
	local, public := twoListeners(t)

	mcp := func(method string, params any) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		res, err := http.Post(local+"/mcp", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out struct {
			Result map[string]any `json:"result"`
			Error  any            `json:"error"`
		}
		json.NewDecoder(res.Body).Decode(&out)
		if out.Error != nil {
			t.Fatalf("%s: %v", method, out.Error)
		}
		return out.Result
	}
	text := func(r map[string]any, key string) string {
		b, _ := json.Marshal(r[key])
		var items []struct {
			Text    string `json:"text"`
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		json.Unmarshal(b, &items)
		if len(items) != 1 {
			t.Fatalf("want one %s, got %s", key, b)
		}
		return items[0].Text + items[0].Content.Text
	}
	if !strings.Contains(string(mustMarshal(mcp("initialize", map[string]any{}))), `"prompts"`) {
		t.Fatal("MCP doesn't announce prompts")
	}
	if p := mustMarshal(mcp("prompts/list", map[string]any{})); !strings.Contains(string(p), `"author-app"`) {
		t.Fatalf("prompts/list: %s", p)
	}
	if got := text(mcp("prompts/get", map[string]any{"name": "author-app"}), "messages"); got != guide {
		t.Fatal("the MCP prompt differs from the repository's guide")
	}
	if got := text(mcp("resources/read", map[string]any{"uri": "oap://skills/author-app"}), "contents"); got != guide {
		t.Fatal("the MCP resource differs from the repository's guide")
	}
	if got := text(mcp("resources/read", map[string]any{"uri": "oap://schema/application"}), "contents"); got != schema {
		t.Fatal("the MCP schema differs from the repository's schema")
	}
	list := string(mustMarshal(mcp("resources/list", map[string]any{})))
	for _, uri := range []string{"oap://skills/author-app", "oap://schema/application", "oap://briefing"} {
		if !strings.Contains(list, uri) {
			t.Errorf("resources/list has no %s", uri)
		}
	}

	get := func(base, path string) (int, string) {
		t.Helper()
		res, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if code, body := get(local, agentapi.SkillsPath+"author-app.md"); code != 200 || body != guide {
		t.Fatalf("HTTPS guide: %d", code)
	}
	if code, body := get(local, agentapi.SchemaPath); code != 200 || body != schema {
		t.Fatalf("HTTPS schema: %d", code)
	}
	if code, _ := get(local, agentapi.SkillsPath+"nope.md"); code != http.StatusNotFound {
		t.Fatalf("unknown skill: %d", code)
	}
	for _, p := range []string{agentapi.SkillsPath + "author-app.md", agentapi.SchemaPath} {
		if code, _ := get(public, p); code != http.StatusUnauthorized {
			t.Errorf("%s without a token on the public listener: %d", p, code)
		}
	}

	dir := t.TempDir()
	if code, out := cli(t, dir, map[string]string{"OAP_AGENT_URL": local}, "skill"); code != 0 || out != guide {
		t.Fatalf("oapctl skill: %d", code)
	}
	if code, out := cli(t, dir, map[string]string{"OAP_AGENT_URL": local}, "skill", "schema"); code != 0 || out != schema {
		t.Fatalf("oapctl skill schema: %d", code)
	}
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// TestGuideExampleIsValid: the oap.yaml example in the guide is a spec OAP
// accepts, so agents copying its shape start from something valid.
func TestGuideExampleIsValid(t *testing.T) {
	sk, _ := skills.Get("author-app")
	blocks := regexp.MustCompile("(?s)```yaml\n(.*?)```").FindAllStringSubmatch(sk.Markdown, -1)
	if len(blocks) == 0 {
		t.Fatal("the guide has no yaml example")
	}
	for _, b := range blocks {
		tree, probs := desiredyaml.Parse([]byte(b[1]))
		if len(probs) == 0 {
			_, probs = spec.ParseApplication(tree)
		}
		if len(probs) > 0 {
			t.Errorf("the guide's example isn't a valid spec: %v\n%s", probs, b[1])
		}
	}
}
