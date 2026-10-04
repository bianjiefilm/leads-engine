package campaignmotion

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	secretPhone    = "13900002222"
	secretEmail    = "secret.customer@example.com"
	secretChat     = "聊天原文-不应出现-XYZ"
	secretDM       = "私聊全文-不应出现-ABC"
	secretLead     = "lead-unauth-9f3a"
	secretLeadName = "未授权客户甲"
	secretCard     = "PRIVACY-4401"
	secretAddress  = "秘密住址-不应出现"
	secretFact     = "未确认配方-不应出现"
	secretAsset    = "asset-secret-unauth"
	secretPrompt   = "PROMPT-SECRET-不应出现"
	secretRender   = "https://evil.example/video.mp4"
	revenue        = int64(987654321)
)

func validDigest() string { return "sha256:" + strings.Repeat("ab", 32) }

func handoffRaw(t *testing.T, campaignID string, digest string, extra string) []byte {
	t.Helper()
	body := `{
		"campaign_goal": "春季上新目标 ` + secretPhone + `",
		"target_audience": "门店店主 ` + secretEmail + `",
		"approved_facts": [
			{"key":"product","value":"冷萃。` + secretChat + `","confirmed":true},
			{"key":"recipe","value":"` + secretFact + `","confirmed":false},
			{"key":"phone","value":"` + secretPhone + `","confirmed":true}
		],
		"assets": [
			{"id":"asset_ok_1","authorized":true},
			{"id":"` + secretAsset + `","authorized":false}
		],
		"cta": "到店试喝",
		"channels": ["抖音", "抖音", "视频号", "小红书"],
		"budget_attribution_id": "budget_spring_1",
		"motion": {
			"project_id": "prj_motion_1",
			"revision_id": "rev_motion_1",
			"campaign_id": "` + campaignID + `",
			"digest": "` + digest + `"
		},
		"chat": "` + secretChat + `",
		"messages": [{"role":"user","text":"` + secretDM + `"}],
		"phone": "` + secretPhone + `",
		"email": "` + secretEmail + `",
		"unauthorized_leads": [{"id":"` + secretLead + `","name":"` + secretLeadName + `","phone":"13900003333"}],
		"customer_privacy": {"id_card":"` + secretCard + `","address":"` + secretAddress + `"},
		"conversion": {"leads":4,"conversions":1,"revenue_cents":` + "987654321" + `},
		"variants": [{"channel":"抖音","prompt":"` + secretPrompt + `","render":"` + secretRender + `"}]
	}`
	if extra != "" {
		body = body[:len(body)-2] + "," + extra + "}"
	}
	return []byte(body)
}

type panicWriter struct{}

func (panicWriter) WriteCreativeProject(string, string, []byte) error {
	panic("creative project write")
}

func TestExportDropsPrivacyAndKeepsOnlyAllowedFields(t *testing.T) {
	const campaignID = "led_campaign_1"
	got, err := Prepare(campaignID, handoffRaw(t, campaignID, validDigest(), ""), panicWriter{})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got.Export)
	for _, secret := range []string{
		secretPhone, secretEmail, secretChat, secretDM, secretLead, secretLeadName,
		secretCard, secretAddress, secretFact, secretAsset, secretPrompt, secretRender,
		"987654321", "13900003333",
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("export contains %s: %s", secret, text)
		}
	}
	var keys map[string]any
	if err := json.Unmarshal(got.Export, &keys); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"phone", "email", "chat", "messages", "unauthorized_leads", "customer_privacy",
		"conversion", "revenue_cents", "prompt", "render", "timeline", "leads", "name",
	} {
		if _, ok := keys[forbidden]; ok {
			t.Fatalf("export key %s: %s", forbidden, text)
		}
	}
	if got.Document.CampaignGoal != "春季上新目标" || got.Document.TargetAudience != "门店店主" {
		t.Fatalf("scrubbed text: %+v", got.Document)
	}
	if len(got.Document.ApprovedFacts) != 1 || got.Document.ApprovedFacts[0].Key != "product" || got.Document.ApprovedFacts[0].Value != "冷萃。" {
		t.Fatalf("facts: %+v", got.Document.ApprovedFacts)
	}
	if len(got.Document.AuthorizedAssetIDs) != 1 || got.Document.AuthorizedAssetIDs[0] != "asset_ok_1" {
		t.Fatalf("assets: %+v", got.Document.AuthorizedAssetIDs)
	}
	if got.Document.CTA != "到店试喝" || got.Document.BudgetAttributionID != "budget_spring_1" {
		t.Fatalf("doc: %+v", got.Document)
	}
	if got.Local.Leads != 4 || got.Local.Conversions != 1 || got.Local.RevenueCents != revenue {
		t.Fatalf("local conversion = %+v", got.Local)
	}
	local, err := json.Marshal(got.Local)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(local), "987654321") {
		t.Fatalf("conversion left the leads side: %s", local)
	}
	if got.ModelCalls != 0 || got.CreativeProjectWrites != 0 || got.PieceGenerated || got.PieceSent {
		t.Fatalf("result claims production: %+v", got)
	}
}

func TestChannelsDoNotScaleModelCalls(t *testing.T) {
	raw := []byte(`{
		"campaign_goal":"多渠道只记名字",
		"target_audience":"到店客人",
		"cta":"查看菜单",
		"channels":["抖音","抖音","视频号","小红书","微信"],
		"budget_attribution_id":"budget_spring_1",
		"motion":{"project_id":"prj_motion_1","revision_id":"rev_motion_1","campaign_id":"led_campaign_1","digest":"` + validDigest() + `"}
	}`)
	got, err := Prepare("led_campaign_1", raw, panicWriter{})
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelCalls != 0 || got.CreativeProjectWrites != 0 || len(got.Document.Channels) != 4 {
		t.Fatalf("channels=%v model=%d writes=%d", got.Document.Channels, got.ModelCalls, got.CreativeProjectWrites)
	}
	if len(got.Document.Variants) != len(got.Document.Channels) {
		t.Fatalf("variants=%+v", got.Document.Variants)
	}
	for _, variant := range got.Document.Variants {
		encoded, err := json.Marshal(variant)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 1 || fields["channel"] == nil {
			t.Fatalf("variant is not a channel name: %s", encoded)
		}
	}
}

func TestMotionDigestAndShape(t *testing.T) {
	base := func(digest, extra string) []byte {
		motion := `{"project_id":"prj_motion_1","revision_id":"rev_motion_1","campaign_id":"led_campaign_1","digest":"` + digest + `"`
		if extra != "" {
			motion += "," + extra
		}
		motion += "}"
		return []byte(`{
			"campaign_goal":"只校验引用",
			"channels":["抖音"],
			"budget_attribution_id":"budget_spring_1",
			"motion":` + motion + `
		}`)
	}
	if _, err := Prepare("led_campaign_1", base(validDigest(), ""), nil); err != nil {
		t.Fatal(err)
	}
	bad := []string{
		"sha256:" + strings.Repeat("AB", 32),
		"sha256:" + strings.Repeat("a", 63),
		"sha256:" + strings.Repeat("a", 65),
		"sha1:" + strings.Repeat("a", 64),
		strings.Repeat("a", 64),
		"sha256:" + strings.Repeat("a", 32) + "GG" + strings.Repeat("a", 30),
	}
	for _, digest := range bad {
		_, err := Prepare("led_campaign_1", base(digest, ""), nil)
		if err != ErrDigest {
			t.Fatalf("digest %s err=%v", digest, err)
		}
	}
	_, err := Prepare("led_campaign_1", base(validDigest(), `"timeline":{"ast":"secret-timeline"}`), nil)
	if err != ErrMotion {
		t.Fatalf("extra motion key err=%v", err)
	}
	mismatch := []byte(`{
		"campaign_goal":"活动对不上",
		"channels":["抖音"],
		"budget_attribution_id":"budget_spring_1",
		"motion":{"project_id":"prj_motion_1","revision_id":"rev_motion_1","campaign_id":"led_other","digest":"` + validDigest() + `"}
	}`)
	_, err = Prepare("led_campaign_1", mismatch, nil)
	if err != ErrMotion {
		t.Fatalf("campaign mismatch err=%v", err)
	}
	omitted := []byte(`{
		"campaign_goal":"补上活动 id",
		"channels":["抖音"],
		"budget_attribution_id":"budget_spring_1",
		"motion":{"project_id":"prj_motion_1","revision_id":"rev_motion_1","digest":"` + validDigest() + `"}
	}`)
	got, err := Prepare("led_campaign_1", omitted, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Document.Motion.CampaignID != "led_campaign_1" {
		t.Fatalf("motion=%+v", got.Document.Motion)
	}
	for _, raw := range [][]byte{
		[]byte(`{"campaign_goal":"空工程","channels":["抖音"],"budget_attribution_id":"budget_spring_1","motion":{"project_id":"","revision_id":"rev_motion_1","campaign_id":"led_campaign_1","digest":"` + validDigest() + `"}}`),
		[]byte(`{"campaign_goal":"空版本","channels":["抖音"],"budget_attribution_id":"budget_spring_1","motion":{"project_id":"prj_motion_1","revision_id":"","campaign_id":"led_campaign_1","digest":"` + validDigest() + `"}}`),
		[]byte(`{"campaign_goal":"空白工程","channels":["抖音"],"budget_attribution_id":"budget_spring_1","motion":{"project_id":"   ","revision_id":"rev_motion_1","digest":"` + validDigest() + `"}}`),
		[]byte(`{"campaign_goal":"空白版本","channels":["抖音"],"budget_attribution_id":"budget_spring_1","motion":{"project_id":"prj_motion_1","revision_id":"  ","digest":"` + validDigest() + `"}}`),
	} {
		if _, err := Prepare("led_campaign_1", raw, nil); err != ErrMotion {
			t.Fatalf("empty motion id err=%v body=%s", err, raw)
		}
	}
	var motion map[string]any
	raw, _ := json.Marshal(got.Document.Motion)
	if err := json.Unmarshal(raw, &motion); err != nil {
		t.Fatal(err)
	}
	if len(motion) != 4 || motion["project_id"] == nil || motion["revision_id"] == nil || motion["campaign_id"] == nil || motion["digest"] == nil {
		t.Fatalf("motion keys=%v", motion)
	}
}

func TestNoticeDoesNotClaimAFinishedPiece(t *testing.T) {
	for _, phrase := range []string{"已生成", "已送出", "已经生成", "已经送出"} {
		if strings.Contains(Notice, phrase) {
			t.Fatalf("notice claims a finished piece: %s", Notice)
		}
	}
	if !strings.Contains(Notice, "没有生成成片") || !strings.Contains(Notice, "没有送出成片") {
		t.Fatal(Notice)
	}
}

func TestPackageDoesNotCallAModel(t *testing.T) {
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range matches {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		src := string(body)
		for _, bad := range []string{"net/http", "openai", "http.Post", "http.Get", "writer.WriteCreativeProject"} {
			if strings.Contains(src, bad) {
				t.Fatalf("%s contains %s", name, bad)
			}
		}
	}
}
