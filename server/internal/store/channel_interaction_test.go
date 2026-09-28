package store

import (
	"strings"
	"testing"
)

func TestChannelReplayRetractAndRevokeDoNotDuplicateLeads(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	grant := ChannelGrantIn{
		Provider: "connector", AccountID: "acct_1", AppID: "app_auth",
		SubjectNS: "connector.subject", MessageNS: "connector.message", PostNS: "connector.post",
		Capabilities: []string{"comment.read", "message.read"},
	}
	if err := s.SaveChannelGrant("tnt_1", grant); err != nil {
		t.Fatalf("grant: %v", err)
	}
	ev := channelEvent("evt_1", "direct_message", true)
	first, err := s.IngestChannelEvent("tnt_1", ev)
	if err != nil || first.CandidateID == "" || first.AutoReach || first.PhoneMarketing || first.SMSMarketing {
		t.Fatalf("first: %+v %v", first, err)
	}
	replay, err := s.IngestChannelEvent("tnt_1", ev)
	if err != nil || !replay.Idempotent || replay.InteractionID != first.InteractionID || replay.CandidateID != first.CandidateID {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if n := channelCandidateCount(t, s, "tnt_1"); n != 1 {
		t.Fatalf("candidates after replay = %d", n)
	}
	ev.Retracted = true
	retract, err := s.IngestChannelEvent("tnt_1", ev)
	if err != nil || retract.CandidateStatus != "withdrawn" {
		t.Fatalf("retract: %+v %v", retract, err)
	}
	if _, err := s.ConfirmChannelCandidate("tnt_1", first.CandidateID, "mem_1"); err == nil {
		t.Fatal("withdrawn candidate was confirmed")
	}
	if err := s.RevokeChannelGrant("tnt_1", "connector", "acct_1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	ev.Retracted = false
	revived, err := s.IngestChannelEvent("tnt_1", ev)
	if err != nil || revived.Refusal != "permission_revoked" || revived.CandidateID != "" {
		t.Fatalf("revoked replay restored access: %+v %v", revived, err)
	}
}

func TestChannelAccountsAndTenantsStaySeparate(t *testing.T) {
	d := intakeTestDB(t)
	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_2','B','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("tenant b: %v", err)
	}
	s := New(d)
	grant := ChannelGrantIn{
		Provider: "connector", AccountID: "acct_1", AppID: "app_auth",
		SubjectNS: "connector.subject", MessageNS: "connector.message", PostNS: "connector.post",
		Capabilities: []string{"message.read", "reply"},
	}
	if err := s.SaveChannelGrant("tnt_1", grant); err != nil {
		t.Fatal(err)
	}
	other := grant
	other.AccountID = "acct_2"
	if err := s.SaveChannelGrant("tnt_1", other); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveChannelGrant("tnt_2", grant); err != nil {
		t.Fatal(err)
	}
	ev := channelEvent("evt_9", "direct_message", true)
	ev.Phone = "13800000000"
	ev.Nickname = "小王"
	a, err := s.IngestChannelEvent("tnt_1", ev)
	if err != nil {
		t.Fatal(err)
	}
	ev.AccountID = "acct_2"
	b, err := s.IngestChannelEvent("tnt_1", ev)
	if err != nil {
		t.Fatal(err)
	}
	if a.InteractionID == b.InteractionID || a.CandidateID == b.CandidateID {
		t.Fatalf("accounts merged: %+v %+v", a, b)
	}
	ev.AccountID = "acct_1"
	ev.TargetTenantID = "tnt_2"
	c, err := s.IngestChannelEvent("tnt_2", ev)
	if err != nil || c.InteractionID == a.InteractionID {
		t.Fatalf("tenants merged: %+v %v", c, err)
	}
	leadA, err := s.ConfirmChannelCandidate("tnt_1", a.CandidateID, "mem_1")
	if err != nil {
		t.Fatal(err)
	}
	leadB, err := s.ConfirmChannelCandidate("tnt_1", b.CandidateID, "mem_1")
	if err != nil {
		t.Fatal(err)
	}
	if leadA == leadB {
		t.Fatal("same phone across accounts confirmed into one lead")
	}
	var phone string
	if err := d.QueryRow(`SELECT phone FROM contacts WHERE id=(SELECT contact_id FROM leads WHERE id=?)`, leadA).Scan(&phone); err != nil {
		t.Fatal(err)
	}
	if phone != "" {
		t.Fatalf("confirmed contact copied the channel phone %q", phone)
	}
}

func TestChannelReplyFloorBlocksTheOtherBot(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	grant := ChannelGrantIn{
		Provider: "connector", AccountID: "acct_1", AppID: "app_auth",
		SubjectNS: "connector.subject", MessageNS: "connector.message", PostNS: "connector.post",
		Capabilities: []string{"comment.read", "reply"},
	}
	if err := s.SaveChannelGrant("tnt_1", grant); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChannelFloor("tnt_1", "connector", "acct_1", "sub_1", "matrix_bot"); err != nil {
		t.Fatal(err)
	}
	ev := channelEvent("evt_reply", "comment", false)
	ev.WantReply = true
	ev.Score = 100
	got, err := s.IngestChannelEvent("tnt_1", ev)
	if err != nil || got.ReplyAllowed || got.Delivered || got.AutoReach || got.ReplyRefusal != "speaker_conflict" {
		t.Fatalf("reply: %+v %v", got, err)
	}
}

func TestPublishGrantCannotIngestMessages(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	grant := ChannelGrantIn{
		Provider: "connector", AccountID: "acct_1", AppID: "app_auth",
		SubjectNS: "connector.subject", MessageNS: "connector.message", PostNS: "connector.post",
		Capabilities: []string{"publish"},
	}
	if err := s.SaveChannelGrant("tnt_1", grant); err != nil {
		t.Fatal(err)
	}
	got, err := s.IngestChannelEvent("tnt_1", channelEvent("evt_dm", "direct_message", true))
	if err != nil || got.Refusal != "capability_missing" || got.InteractionID != "" {
		t.Fatalf("publish read a message: %+v %v", got, err)
	}
}

func channelEvent(eventID, kind string, intent bool) ChannelEventIn {
	purpose := "general_qa"
	if intent {
		purpose = "sales_inquiry"
	}
	return ChannelEventIn{
		TargetTenantID: "tnt_1", Provider: "connector", AccountID: "acct_1", AppID: "app_auth",
		SubjectNS: "connector.subject", MessageNS: "connector.message", PostNS: "connector.post",
		EventID: eventID, Kind: kind, SubjectID: "sub_1", Nickname: "小王",
		Text: "客户私信全文不应进日志", ExplicitIntent: intent, Purpose: purpose,
	}
}

func channelCandidateCount(t *testing.T, s *Store, tenant string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM channel_lead_candidates WHERE tenant_id=?`, tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestChannelListOmitsUsingTextAsKey(t *testing.T) {
	d := intakeTestDB(t)
	s := New(d)
	if err := s.SaveChannelGrant("tnt_1", ChannelGrantIn{
		Provider: "connector", AccountID: "acct_1", AppID: "app_auth",
		SubjectNS: "connector.subject", MessageNS: "connector.message", PostNS: "connector.post",
		Capabilities: []string{"comment.read"},
	}); err != nil {
		t.Fatal(err)
	}
	ev := channelEvent("evt_list", "comment", false)
	if _, err := s.IngestChannelEvent("tnt_1", ev); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListChannelInteractions("tnt_1")
	if err != nil || len(rows) != 1 || rows[0].Verification != "unverified" || len(rows[0].LiveProviders) != 0 {
		t.Fatalf("list: %+v %v", rows, err)
	}
	if strings.Contains(rows[0].Path, ev.Text) {
		t.Fatalf("path leaked text: %s", rows[0].Path)
	}
}
