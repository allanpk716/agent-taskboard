package outlet_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/outlet"
)

func Test出口URL只由白名单base和受校验botID拼接(t *testing.T) {
	builder, err := outlet.NewURLBuilder([]string{
		"https://bot.example.test/base/",
		"https://backup-bot.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := builder.Build("bot01")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != "bot.example.test" ||
		parsed.Path != "/base/api/v1/bots/bot01/webhook" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		t.Fatalf("URL = %s，想落在第一个白名单 base 下且无查询或片段", raw)
	}
}

func Test出口URLbase无结尾斜杠时也会规范化(t *testing.T) {
	builder, err := outlet.NewURLBuilder([]string{"https://bot.example.test/base"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := builder.Build("bot01")
	if err != nil {
		t.Fatal(err)
	}
	if raw != "https://bot.example.test/base/api/v1/bots/bot01/webhook" {
		t.Fatalf("URL = %s，想规范化 base 的结尾斜杠", raw)
	}
}

func Test出口URL构造拒绝非法base和botID(t *testing.T) {
	bases := []string{
		"http://bot.example.test",
		"bot.example.test/path",
		"https://user:pass@bot.example.test",
		"https://bot.example.test?next=/admin",
		"https://bot.example.test#webhook",
		"https://bot.example.test/../other",
	}
	for _, base := range bases {
		if _, err := outlet.NewURLBuilder([]string{base}); err == nil {
			t.Fatalf("白名单 base %q 应拒绝", base)
		}
	}
	if _, err := outlet.NewURLBuilder(nil); err == nil {
		t.Fatal("空白名单应拒绝")
	}

	builder, err := outlet.NewURLBuilder([]string{"https://bot.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, botID := range []string{"", "Bot01", "../bot", "bot%2f", "bot?x=1", strings.Repeat("b", 65)} {
		if raw, err := builder.Build(botID); err == nil {
			t.Fatalf("bot_id %q 不应拼出 URL %s", botID, raw)
		}
	}
}
