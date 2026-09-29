package matrixhandoff_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bianjiefilm/leads-engine/server/internal/matrixhandoff"
)

func validSelection() matrixhandoff.Selection {
	return matrixhandoff.Selection{
		ActorTenantID:  "ten_a",
		TenantID:       "ten_a",
		CampaignID:     "camp_1",
		AssetID:        "asset_1",
		ContentVersion: 3,
		Purpose:        "campaign_post",
		Legal:          true,
		Source: matrixhandoff.SourceIDs{
			LeadID:     "lead_1",
			SessionID:  "ses_1",
			FollowUpID: "fu_1",
		},
	}
}

func newSvc(t *testing.T) (*matrixhandoff.Service, *matrixhandoff.Fixture) {
	t.Helper()
	fx := matrixhandoff.NewFixture()
	return matrixhandoff.New(fx), fx
}

func assertEffects(t *testing.T, svc *matrixhandoff.Service) {
	t.Helper()
	got, err := json.Marshal(svc.Effects())
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"lead_writes":0,"reply_bot_starts":0,"charge_writes":0,"charged":null,"billing_passed":false}`
	if string(got) != want {
		t.Fatalf("effects %s", got)
	}
}

func storedBlob(t *testing.T, svc *matrixhandoff.Service, fx *matrixhandoff.Fixture) string {
	t.Helper()
	var b strings.Builder
	for _, d := range svc.Drafts() {
		raw, err := svc.Document(d.ID)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(raw)
		note, err := svc.Notice(d.ID)
		if err != nil {
			t.Fatal(err)
		}
		nb, err := json.Marshal(note)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(nb)
		if strings.TrimSpace(d.Status) == "" || strings.TrimSpace(d.Copy) == "" {
			t.Fatalf("empty draft copy: %+v", d)
		}
		if strings.Contains(d.Status, "发布成功") || strings.Contains(d.Copy, "发布成功") || strings.Contains(d.Status, "白标经营链完成") || strings.Contains(d.Copy, "白标经营链完成") {
			t.Fatalf("copy claims success: %s %s", d.Status, d.Copy)
		}
		if d.LiveReceipt || d.ChainComplete || d.PublishApproved || d.PublishExecuted {
			t.Fatalf("publish flags: %+v", d)
		}
	}
	for _, line := range svc.Logs() {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if fx != nil {
		pb, err := json.Marshal(fx.Payloads)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(pb)
	}
	return b.String()
}

func assertAbsent(t *testing.T, blob string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(blob, secret) {
			t.Fatalf("leaked %q in %s", secret, blob)
		}
	}
}

func docMap(t *testing.T, svc *matrixhandoff.Service, id string) map[string]any {
	t.Helper()
	raw, err := svc.Document(id)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// 生产代码若在相同键上改写已存来源或另开草稿，这条会失败。
func TestRepeatSelectionReturnsSameDraftAndDoesNotRewriteStoredContent(t *testing.T) {
	svc, fx := newSvc(t)
	first, err := svc.Select(validSelection())
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "draft_recorded" || first.Copy != "已记下草稿引用" {
		t.Fatalf("draft copy: %s %s", first.Status, first.Copy)
	}
	againSel := validSelection()
	againSel.Source.LeadID = "lead_2"
	againSel.IntentScore = 100
	againSel.SalesRole = "director"
	second, err := svc.Select(againSel)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Source.LeadID != "lead_1" || second.ContentVersion != 3 || second.ReturnRef != first.ReturnRef {
		t.Fatalf("rewrote draft: %+v", second)
	}
	if fx.Calls != 1 || len(fx.Payloads) != 1 {
		t.Fatalf("fixture calls=%d payloads=%d", fx.Calls, len(fx.Payloads))
	}
	if strings.Contains(second.ReturnRef, "lead_1") || !strings.Contains(second.ReturnRef, second.ID) {
		t.Fatalf("return ref %s", second.ReturnRef)
	}
	raw, err := json.Marshal(fx.Payloads[0])
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 4 {
		t.Fatalf("payload keys %v", payload)
	}
	for _, key := range []string{"source_ids", "asset_id", "version", "return_ref"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("missing %s in %s", key, raw)
		}
	}
	if payload["asset_id"] != "asset_1" || payload["version"] != float64(3) {
		t.Fatalf("payload %s", raw)
	}
	src := payload["source_ids"].(map[string]any)
	if len(src) != 3 || src["lead_id"] != "lead_1" || src["session_id"] != "ses_1" || src["follow_up_id"] != "fu_1" {
		t.Fatalf("source ids %v", src)
	}
	next := validSelection()
	next.ContentVersion = 4
	third, err := svc.Select(next)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID == first.ID || third.ContentVersion != 4 {
		t.Fatalf("version collapsed: %+v", third)
	}
	kept := docMap(t, svc, first.ID)
	if kept["version"] != float64(3) {
		t.Fatalf("stored version %v", kept["version"])
	}
	keptSrc := kept["source_ids"].(map[string]any)
	if keptSrc["lead_id"] != "lead_1" {
		t.Fatalf("stored lead %v", keptSrc["lead_id"])
	}
	if len(svc.Drafts()) != 2 || fx.Calls != 2 {
		t.Fatalf("drafts %d calls %d", len(svc.Drafts()), fx.Calls)
	}
	assertEffects(t, svc)
}

// 生产代码若让两个租户共用一条草稿，这条会失败。
func TestTwoTenantsDoNotShareADraft(t *testing.T) {
	svc, fx := newSvc(t)
	a := validSelection()
	a.Source.LeadID = "lead_a"
	b := validSelection()
	b.ActorTenantID = "ten_b"
	b.TenantID = "ten_b"
	b.Source.LeadID = "lead_b"
	da, err := svc.Select(a)
	if err != nil {
		t.Fatal(err)
	}
	db, err := svc.Select(b)
	if err != nil {
		t.Fatal(err)
	}
	if da.ID == db.ID || da.TenantID != "ten_a" || db.TenantID != "ten_b" {
		t.Fatalf("shared draft %s %s", da.ID, db.ID)
	}
	blobA, err := svc.Document(da.ID)
	if err != nil {
		t.Fatal(err)
	}
	blobB, err := svc.Document(db.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(blobA, "lead_b") || strings.Contains(blobB, "lead_a") || strings.Contains(blobA, "ten_b") || !strings.Contains(blobB, "ten_b") {
		t.Fatalf("tenant leak %s %s", blobA, blobB)
	}
	if len(fx.Payloads) != 2 {
		t.Fatalf("payloads %d", len(fx.Payloads))
	}
	assertEffects(t, svc)
}

// 生产代码若收下电话、邮箱、档案、客户名单或正文，这条会失败。
func TestForbiddenFieldsAreRejectedAndNotStored(t *testing.T) {
	svc, fx := newSvc(t)
	first, err := svc.Select(validSelection())
	if err != nil {
		t.Fatal(err)
	}
	before, err := svc.Document(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secrets := []string{"13800138000", "secret@example.com", "档案全文-机密", "cust_secret_9", "短信正文-机密-XYZ"}
	poison := validSelection()
	poison.Phone = secrets[0]
	poison.Email = secrets[1]
	poison.CRMProfile = secrets[2]
	poison.CustomerList = []string{secrets[3]}
	poison.MessageBody = secrets[4]
	_, err = svc.Select(poison)
	if !errors.Is(err, matrixhandoff.ErrForbidden) {
		t.Fatalf("select poison: %v", err)
	}
	if strings.Contains(err.Error(), secrets[0]) || strings.Contains(err.Error(), secrets[4]) {
		t.Fatalf("error leaks: %v", err)
	}
	after, err := svc.Document(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || len(svc.Drafts()) != 1 || fx.Calls != 1 {
		t.Fatalf("poison rewrote store drafts=%d calls=%d", len(svc.Drafts()), fx.Calls)
	}
	smuggle := validSelection()
	smuggle.AssetID = "asset_smuggle"
	smuggle.Source.LeadID = "短信正文-机密-XYZ"
	_, err = svc.Select(smuggle)
	if !errors.Is(err, matrixhandoff.ErrForbidden) {
		t.Fatalf("smuggle: %v", err)
	}
	nav := validSelection()
	nav.AssetID = ""
	nav.Phone = secrets[0]
	_, err = svc.Jump(&nav)
	if !errors.Is(err, matrixhandoff.ErrForbidden) {
		t.Fatalf("jump poison: %v", err)
	}
	ev := matrixhandoff.Event{
		ProviderEventID: "pev_poison",
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         first.ID,
		Kind:            "result",
		Phone:           secrets[0],
		Email:           secrets[1],
		CRMProfile:      secrets[2],
		CustomerList:    []string{secrets[3]},
		MessageBody:     secrets[4],
		MatrixPlan:      "plan_should_not_stick",
	}
	_, err = svc.ApplyEvent(ev)
	if !errors.Is(err, matrixhandoff.ErrForbidden) {
		t.Fatalf("event poison: %v", err)
	}
	afterEvent, err := svc.Document(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterEvent != before || len(svc.Drafts()) != 1 {
		t.Fatal("poison event changed the draft")
	}
	assertAbsent(t, storedBlob(t, svc, fx), secrets...)
	assertAbsent(t, storedBlob(t, svc, fx), "plan_should_not_stick")
	for _, bad := range []string{`"phone"`, `"email"`, `"crm_profile"`, `"customer_list"`, `"message_body"`} {
		if strings.Contains(before, bad) {
			t.Fatalf("document has %s: %s", bad, before)
		}
	}
	assertEffects(t, svc)
}

// 生产代码若把意向分、销售角色或生产授权当成发布，这条会失败。
func TestIntentScoreAndSalesRoleDoNotApproveOrExecutePublish(t *testing.T) {
	svc, _ := newSvc(t)
	sel := validSelection()
	sel.IntentScore = 100
	sel.SalesRole = "director"
	sel.StartReplyBot = true
	sel.WantCharge = true
	sel.ProductionAuthorized = true
	got, err := svc.Select(sel)
	if err != nil {
		t.Fatal(err)
	}
	if got.PublishApproved || got.PublishExecuted || got.LiveReceipt || got.ChainComplete {
		t.Fatalf("authority published: %+v", got)
	}
	raw, err := svc.Document(got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "director") || strings.Contains(raw, "发布成功") {
		t.Fatalf("stored authority: %s", raw)
	}
	labels := matrixhandoff.Evidence()
	if labels.Production != "NOT_AUTHORIZED" {
		t.Fatalf("production %s", labels.Production)
	}
	assertEffects(t, svc)
}

// 生产代码若在没选中内容时建计划或草稿，这条会失败。
func TestTopNavJumpWithoutContentCreatesNoPlanOrDraft(t *testing.T) {
	svc, fx := newSvc(t)
	got, err := svc.Jump(nil)
	if err != nil || got.DraftCreated || got.PlanCreated || got.Draft != nil || len(svc.Drafts()) != 0 {
		t.Fatalf("nil jump: %+v err=%v drafts=%d", got, err, len(svc.Drafts()))
	}
	empty := validSelection()
	empty.AssetID = ""
	got, err = svc.Jump(&empty)
	if err != nil || got.DraftCreated || got.PlanCreated || len(svc.Drafts()) != 0 || fx.Calls != 0 {
		t.Fatalf("empty jump: %+v err=%v calls=%d", got, err, fx.Calls)
	}
	illegal := validSelection()
	illegal.Legal = false
	_, err = svc.Select(illegal)
	if !errors.Is(err, matrixhandoff.ErrNotLegal) || len(svc.Drafts()) != 0 || fx.Calls != 0 {
		t.Fatalf("illegal asset: %v drafts=%d", err, len(svc.Drafts()))
	}
	missing := validSelection()
	missing.ContentVersion = 0
	_, err = svc.Select(missing)
	if !errors.Is(err, matrixhandoff.ErrNoContent) || len(svc.Drafts()) != 0 {
		t.Fatalf("missing version: %v", err)
	}
	sel := validSelection()
	got, err = svc.Jump(&sel)
	if err != nil || !got.DraftCreated || got.PlanCreated || got.Draft == nil || got.Draft.MatrixPlan != "" || len(svc.Drafts()) != 1 {
		t.Fatalf("selected jump created a plan: %+v err=%v", got, err)
	}
	assertEffects(t, svc)
}

// 生产代码若把未知触达写成 0，或让重复事件改写引用，这条会失败。
func TestFixtureReportsPersistRestrictedRefsAndUnknownMetricsStayUnknown(t *testing.T) {
	svc, _ := newSvc(t)
	draft, err := svc.Select(validSelection())
	if err != nil {
		t.Fatal(err)
	}
	initial := docMap(t, svc, draft.ID)
	if initial["reach"] != nil || initial["likes"] != nil || initial["leads"] != nil || initial["matrix_plan"] != "" {
		t.Fatalf("new draft metrics %#v", initial)
	}
	zero := 0
	first, err := svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: "pev_metrics",
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         draft.ID,
		Kind:            "result",
		MatrixPlan:      "plan_1",
		MatrixItem:      "item_1",
		ExternalPost:    "post_1",
		Reach:           &zero,
		FixtureSuccess:  true,
	})
	if err != nil || first.CreatedDraft || first.CreatedLead || first.Idempotent || first.LiveReceipt || first.ChainComplete || first.PublishExecuted {
		t.Fatalf("first result: %+v %v", first, err)
	}
	seen := docMap(t, svc, draft.ID)
	if seen["matrix_plan"] != "plan_1" || seen["matrix_item"] != "item_1" || seen["external_post"] != "post_1" {
		t.Fatalf("refs %#v", seen)
	}
	if seen["reach"] != float64(0) || seen["likes"] != nil || seen["leads"] != nil {
		t.Fatalf("metrics reach=%#v likes=%#v leads=%#v", seen["reach"], seen["likes"], seen["leads"])
	}
	likesZero := 0
	rawBefore, err := svc.Document(draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: "pev_metrics",
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         draft.ID,
		Kind:            "result",
		MatrixPlan:      "plan_2",
		Likes:           &likesZero,
		FixtureSuccess:  true,
	})
	if err != nil || !second.Idempotent || second.CreatedDraft || second.CreatedLead {
		t.Fatalf("duplicate: %+v %v", second, err)
	}
	rawAfter, err := svc.Document(draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rawBefore != rawAfter || len(svc.Drafts()) != 1 {
		t.Fatalf("duplicate rewrote document %s", rawAfter)
	}
	if svc.PublishPermitted("ten_a") {
		t.Fatal("result granted publish")
	}
	assertEffects(t, svc)
}

// 生产代码若把夹具成功写成发布成功或白标经营链完成，这条会失败。
func TestFixtureSuccessIsNotLiveReceiptOrChainComplete(t *testing.T) {
	svc, fx := newSvc(t)
	fx.Published = true
	fx.Outcome = "发布成功"
	draft, err := svc.Select(validSelection())
	if err != nil {
		t.Fatal(err)
	}
	if !draft.FixtureAccepted || draft.LiveReceipt || draft.ChainComplete || draft.PublishExecuted {
		t.Fatalf("ack adopted: %+v", draft)
	}
	_, err = svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: "pev_phrase",
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         draft.ID,
		Kind:            "result",
		FixtureSuccess:  true,
		Outcome:         "白标经营链完成",
	})
	if err != nil {
		t.Fatal(err)
	}
	blob := storedBlob(t, svc, nil)
	assertAbsent(t, blob, "发布成功", "白标经营链完成")
	seen := docMap(t, svc, draft.ID)
	if seen["live_receipt"] != false || seen["chain_complete"] != false || seen["publish_approved"] != false || seen["publish_executed"] != false || seen["fixture_accepted"] != true {
		t.Fatalf("flags %#v", seen)
	}
	assertEffects(t, svc)
}

// 生产代码若接受跨租户行动者，这条会失败。
func TestCrossTenantActorIsRejected(t *testing.T) {
	svc, fx := newSvc(t)
	sel := validSelection()
	sel.ActorTenantID = "ten_b"
	_, err := svc.Select(sel)
	if !errors.Is(err, matrixhandoff.ErrCrossTenant) || len(svc.Drafts()) != 0 || fx.Calls != 0 {
		t.Fatalf("cross select: %v drafts=%d", err, len(svc.Drafts()))
	}
	owned, err := svc.Select(validSelection())
	if err != nil {
		t.Fatal(err)
	}
	before, err := svc.Document(owned.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: "pev_cross",
		ActorTenantID:   "ten_b",
		TenantID:        "ten_a",
		DraftID:         owned.ID,
		Kind:            "revoke",
		RestorePublish:  true,
		MatrixPlan:      "plan_cross",
	})
	if !errors.Is(err, matrixhandoff.ErrCrossTenant) {
		t.Fatalf("cross event: %v", err)
	}
	after, err := svc.Document(owned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || svc.PublishRevoked("ten_a") || svc.PublishRevoked("ten_b") || svc.PublishPermitted("ten_a") || len(svc.Drafts()) != 1 {
		t.Fatal("cross-tenant event changed permission or content")
	}
	blank := validSelection()
	blank.ActorTenantID = ""
	_, err = svc.Select(blank)
	if !errors.Is(err, matrixhandoff.ErrCrossTenant) {
		t.Fatalf("blank actor: %v", err)
	}
	assertEffects(t, svc)
}

// 生产代码若对同一提供者事件再建线索或草稿，这条会失败。
func TestDuplicateResultEventDoesNotCreateLeadOrDraft(t *testing.T) {
	svc, _ := newSvc(t)
	draft, err := svc.Select(validSelection())
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ApplyEvent(matrixhandoff.Event{
		ActorTenantID: "ten_a",
		TenantID:      "ten_a",
		DraftID:       draft.ID,
		Kind:          "result",
	})
	if !errors.Is(err, matrixhandoff.ErrEventID) || len(svc.Drafts()) != 1 {
		t.Fatalf("empty event id: %v", err)
	}
	_, err = svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: "pev_missing",
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         "d_missing",
		Kind:            "result",
		MatrixPlan:      "plan_missing",
	})
	if !errors.Is(err, matrixhandoff.ErrNotFound) || len(svc.Drafts()) != 1 {
		t.Fatalf("missing draft: %v drafts=%d", err, len(svc.Drafts()))
	}
	if strings.Contains(storedBlob(t, svc, nil), "plan_missing") {
		t.Fatal("missing result created a plan ref")
	}
	res, err := svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: "pev_once",
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         draft.ID,
		Kind:            "result",
		MatrixPlan:      "plan_once",
	})
	if err != nil || res.CreatedDraft || res.CreatedLead || res.Idempotent {
		t.Fatalf("first: %+v %v", res, err)
	}
	res, err = svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: "pev_once",
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         "d_other",
		Kind:            "result",
		MatrixPlan:      "plan_other",
	})
	if err != nil || !res.Idempotent || res.CreatedDraft || res.CreatedLead || len(svc.Drafts()) != 1 {
		t.Fatalf("second: %+v %v drafts=%d", res, err, len(svc.Drafts()))
	}
	seen := docMap(t, svc, draft.ID)
	if seen["matrix_plan"] != "plan_once" {
		t.Fatalf("plan %v", seen["matrix_plan"])
	}
	path := svc.LeadPath(matrixhandoff.LeadPath{LeadID: "lead_1", SessionID: "ses_1", FollowUpID: "fu_1"})
	if !path.Usable || path.LeadID != "lead_1" || path.SessionID != "ses_1" || path.FollowUpID != "fu_1" {
		t.Fatalf("lead path: %+v", path)
	}
	assertEffects(t, svc)
}

// 生产代码若在矩阵失败时关掉线索路径或写入计数，这条会失败。
func TestMatrixClientErrorLeavesLeadPathUsable(t *testing.T) {
	svc, fx := newSvc(t)
	draft, err := svc.Select(validSelection())
	if err != nil {
		t.Fatal(err)
	}
	fx.Err = errors.New("matrix down")
	again, err := svc.Select(validSelection())
	if err != nil || again.ID != draft.ID || fx.Calls != 1 {
		t.Fatalf("idempotent during outage: %+v %v calls=%d", again, err, fx.Calls)
	}
	next := validSelection()
	next.AssetID = "asset_2"
	next.StartReplyBot = true
	next.WantCharge = true
	_, err = svc.Select(next)
	if !errors.Is(err, matrixhandoff.ErrClient) {
		t.Fatalf("client error: %v", err)
	}
	if len(svc.Drafts()) != 1 || svc.Drafts()[0].ID != draft.ID || len(fx.Payloads) != 1 || fx.Calls != 2 {
		t.Fatalf("drafts=%d payloads=%d calls=%d", len(svc.Drafts()), len(fx.Payloads), fx.Calls)
	}
	path := svc.LeadPath(matrixhandoff.LeadPath{LeadID: "lead_9", SessionID: "ses_9", FollowUpID: "fu_9"})
	if !path.Usable || path.LeadID != "lead_9" || path.SessionID != "ses_9" || path.FollowUpID != "fu_9" {
		t.Fatalf("path closed: %+v", path)
	}
	jump, err := svc.Jump(nil)
	if err != nil || jump.DraftCreated || len(svc.Drafts()) != 1 {
		t.Fatalf("jump after error: %+v %v", jump, err)
	}
	bare := matrixhandoff.New(nil)
	_, err = bare.Select(validSelection())
	if !errors.Is(err, matrixhandoff.ErrClient) || len(bare.Drafts()) != 0 || !bare.LeadPath(matrixhandoff.LeadPath{LeadID: "lead_9"}).Usable {
		t.Fatalf("nil client: %v", err)
	}
	assertEffects(t, svc)
	assertEffects(t, bare)
	labels := matrixhandoff.Evidence()
	if labels.ServiceProvider != "NOT_VERIFIED" || labels.Billing != "NOT_VERIFIED" || labels.Production != "NOT_AUTHORIZED" {
		t.Fatalf("labels upgraded: %+v", labels)
	}
}

// 生产代码若用重放、编辑或撤回恢复发布权，这条会失败。
func TestRevokeReplayEditWithdrawDoNotRestorePublishPermission(t *testing.T) {
	svc, _ := newSvc(t)
	draft, err := svc.Select(validSelection())
	if err != nil {
		t.Fatal(err)
	}
	if svc.PublishPermitted("ten_a") || svc.PublishRevoked("ten_a") {
		t.Fatal("publish started permitted")
	}
	before, err := svc.Document(draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: "pev_publish_kind",
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         draft.ID,
		Kind:            "publish",
		RestorePublish:  true,
		FixtureSuccess:  true,
	})
	if !errors.Is(err, matrixhandoff.ErrEventKind) {
		t.Fatalf("publish kind: %v", err)
	}
	mid, err := svc.Document(draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mid != before || svc.PublishPermitted("ten_a") || svc.PublishRevoked("ten_a") {
		t.Fatal("unknown kind changed permission")
	}
	kinds := []struct {
		id, kind string
	}{
		{"pev_revoke", "revoke"},
		{"pev_replay", "replay"},
		{"pev_edit", "edit"},
		{"pev_withdraw", "withdraw"},
	}
	for _, kind := range kinds {
		res, err := svc.ApplyEvent(matrixhandoff.Event{
			ProviderEventID: kind.id,
			ActorTenantID:   "ten_a",
			TenantID:        "ten_a",
			DraftID:         draft.ID,
			Kind:            kind.kind,
			RestorePublish:  true,
			FixtureSuccess:  true,
			Outcome:         "发布成功",
		})
		if err != nil {
			t.Fatalf("%s: %v", kind.kind, err)
		}
		if res.PublishPermitted || res.PublishExecuted || res.CreatedDraft || res.CreatedLead || res.LiveReceipt || res.ChainComplete {
			t.Fatalf("%s restored: %+v", kind.kind, res)
		}
		if !svc.PublishRevoked("ten_a") || svc.PublishPermitted("ten_a") {
			t.Fatalf("%s latch: revoked=%v permitted=%v", kind.kind, svc.PublishRevoked("ten_a"), svc.PublishPermitted("ten_a"))
		}
	}
	after, err := svc.Document(draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after != before || len(svc.Drafts()) != 1 {
		t.Fatal("permission events rewrote the draft")
	}
	assertAbsent(t, after, "发布成功")
	assertEffects(t, svc)
}

// 生产代码若把消息正文写进日志、文档或通知，这条会失败。
func TestLogsDocumentAndNoticeOmitMessageBody(t *testing.T) {
	svc, _ := newSvc(t)
	draft, err := svc.Select(validSelection())
	if err != nil {
		t.Fatal(err)
	}
	secret := "短信正文-机密-XYZ"
	if svc.Audit("missing", secret) != "" {
		t.Fatal("missing draft logged")
	}
	line := svc.Audit(draft.ID, secret)
	if strings.Contains(line, secret) || !strings.Contains(line, draft.ID) || !strings.Contains(line, "ten_a") || !strings.Contains(line, "asset_1") || !strings.Contains(line, draft.ReturnRef) {
		t.Fatalf("audit: %s", line)
	}
	if strings.Contains(line, "已记下") || strings.Contains(line, "发布") {
		t.Fatalf("audit is not ids only: %s", line)
	}
	for _, logged := range svc.Logs() {
		if strings.Contains(logged, secret) || strings.Contains(logged, "已记下") {
			t.Fatalf("log store: %s", logged)
		}
	}
	note, err := svc.Notice(draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(note)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 5 || keys["draft_id"] != draft.ID || keys["tenant_id"] != "ten_a" || keys["asset_id"] != "asset_1" || keys["version"] != float64(3) || keys["return_ref"] != draft.ReturnRef {
		t.Fatalf("notice %s", raw)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("notice leaked: %s", raw)
	}
	before, err := svc.Document(draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: "pev_body",
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         draft.ID,
		Kind:            "result",
		MessageBody:     secret,
	})
	if !errors.Is(err, matrixhandoff.ErrForbidden) {
		t.Fatalf("body event: %v", err)
	}
	_, err = svc.ApplyEvent(matrixhandoff.Event{
		ProviderEventID: secret,
		ActorTenantID:   "ten_a",
		TenantID:        "ten_a",
		DraftID:         draft.ID,
		Kind:            "result",
	})
	if !errors.Is(err, matrixhandoff.ErrEventID) {
		t.Fatalf("body event id: %v", err)
	}
	after, err := svc.Document(draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("body event changed the document")
	}
	assertAbsent(t, storedBlob(t, svc, nil), secret)
	assertEffects(t, svc)
}

// 生产代码若把没有私信授权的渠道写成成功，或宣称八平台已接入，这条会失败。
func TestChannelWithoutDMGrantIsUnavailableAndEightPlatformsAreNotIngested(t *testing.T) {
	if matrixhandoff.ClaimsEightPlatformDM() {
		t.Fatal("claimed eight-platform DM ingestion")
	}
	ids := []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8"}
	for _, id := range ids {
		got := matrixhandoff.AssessChannel(matrixhandoff.Channel{ID: id, DMGrant: false})
		if got.ID != id || got.Status != "unavailable" || got.Success || got.DMIngested {
			t.Fatalf("no grant %s: %+v", id, got)
		}
	}
	for _, id := range ids {
		got := matrixhandoff.AssessChannel(matrixhandoff.Channel{ID: id, DMGrant: true})
		if got.Success || got.DMIngested || got.Status != "not_ingested" {
			t.Fatalf("grant ingested %s: %+v", id, got)
		}
	}
	if matrixhandoff.ClaimsEightPlatformDM() {
		t.Fatal("claimed eight-platform DM ingestion after grants")
	}
}

// 生产代码若把证据标成 PASS 或已授权，这条会失败。
func TestEvidenceLabelsStayUnverified(t *testing.T) {
	svc, _ := newSvc(t)
	sel := validSelection()
	sel.ProductionAuthorized = true
	draft, err := svc.Select(sel)
	if err != nil {
		t.Fatal(err)
	}
	labels := matrixhandoff.Evidence()
	if labels.ServiceProvider != "NOT_VERIFIED" || labels.Billing != "NOT_VERIFIED" || labels.Production != "NOT_AUTHORIZED" || labels.HumanAdoption != "UNKNOWN" || labels.Browser != "NOT_RUN" {
		t.Fatalf("labels %+v", labels)
	}
	seen := docMap(t, svc, draft.ID)
	if seen["service_provider"] != "NOT_VERIFIED" || seen["billing"] != "NOT_VERIFIED" || seen["production"] != "NOT_AUTHORIZED" || seen["human_adoption"] != "UNKNOWN" || seen["browser"] != "NOT_RUN" {
		t.Fatalf("document labels %#v", seen)
	}
	if seen["charged"] != nil || seen["billing_passed"] != false || seen["lead_writes"] != float64(0) || seen["reply_bot_starts"] != float64(0) || seen["charge_writes"] != float64(0) {
		t.Fatalf("document effects %#v", seen)
	}
	if seen["schema"] != "matrix-handoff/v1" {
		t.Fatalf("schema %v", seen["schema"])
	}
	assertEffects(t, svc)
}
