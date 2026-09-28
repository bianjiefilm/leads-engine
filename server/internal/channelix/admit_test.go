package channelix

import (
	"strings"
	"testing"
)

// 生产代码若把发布授权当成私信读取，这条会失败。
func TestPublishGrantDoesNotReadMessages(t *testing.T) {
	got := Admit(sampleInput(func(in *Input) {
		in.Grant.Capabilities = []string{CapPublish, CapCommentRead}
		in.Event.Kind = KindMessage
	}))
	if got.OK || got.Refusal != RefusalCapability {
		t.Fatalf("publish+comment.read admitted a direct message: %+v", got)
	}
	if got.CreateInteraction || got.CreateCandidate || got.AutoReach {
		t.Fatalf("refused message still created records: %+v", got)
	}
}

// 生产代码若按昵称或手机号把两个账号收成同一个人，这条会失败。
func TestNicknameAndPhoneDoNotMergeAcrossAccountsOrTenants(t *testing.T) {
	a := SubjectRef{TenantID: "tnt_a", Provider: "connector", AccountID: "acct_1", SubjectID: "sub_1", Nickname: "小王", Phone: "13800000000"}
	b := a
	b.AccountID = "acct_2"
	b.SubjectID = "sub_9"
	if SharesIdentity(a, b) {
		t.Fatal("same nickname and phone across accounts were treated as one subject")
	}
	c := a
	c.TenantID = "tnt_b"
	if SharesIdentity(a, c) {
		t.Fatal("same nickname and phone across tenants were treated as one subject")
	}
	same := a
	if !SharesIdentity(a, same) {
		t.Fatal("the same tenant, provider, account, and subject must stay one subject")
	}
	left := Admit(sampleInput(nil))
	right := Admit(sampleInput(func(in *Input) {
		in.Grant.AccountID = "acct_2"
		in.Event.AccountID = "acct_2"
		in.Event.Nickname = "小王"
		in.Event.Phone = "13800000000"
	}))
	if left.DedupKey == right.DedupKey {
		t.Fatalf("dedup key ignored account: %s", left.DedupKey)
	}
	if strings.Contains(left.DedupKey, "小王") || strings.Contains(left.DedupKey, "13800000000") {
		t.Fatalf("dedup key contains nickname or phone: %s", left.DedupKey)
	}
}

// 生产代码若在没有明确购买意向时建线索候选，这条会失败。
func TestOnlyExplicitSalesInquiryBecomesCandidate(t *testing.T) {
	plain := Admit(sampleInput(nil))
	if !plain.OK || !plain.CreateInteraction || plain.CreateCandidate {
		t.Fatalf("ordinary comment: %+v", plain)
	}
	for _, purpose := range []string{PurposeSupport, PurposeGeneralQA, PurposeAftersales} {
		got := Admit(sampleInput(func(in *Input) {
			in.Event.ExplicitIntent = true
			in.Event.Purpose = purpose
		}))
		if !got.OK || !got.CreateInteraction || got.CreateCandidate {
			t.Fatalf("%s became a lead candidate: %+v", purpose, got)
		}
	}
	sales := Admit(sampleInput(func(in *Input) {
		in.Event.ExplicitIntent = true
		in.Event.Purpose = PurposeSalesInquiry
	}))
	if !sales.CreateCandidate || sales.AutoReach {
		t.Fatalf("explicit sales inquiry: %+v", sales)
	}
}

// 生产代码若把“没有手机号”理解成可以电话或短信营销，这条会失败。
func TestMissingPhoneIsNotPhoneOrSMSMarketing(t *testing.T) {
	got := Admit(sampleInput(func(in *Input) {
		in.Event.Phone = ""
		in.Event.PhoneMarketingConsent = true
		in.Event.SMSMarketingConsent = true
		in.Event.ExplicitIntent = true
		in.Event.Purpose = PurposeSalesInquiry
	}))
	if !got.ChannelContact || got.PhoneMarketing || got.SMSMarketing {
		t.Fatalf("missing phone: %+v", got)
	}
	withPhone := Admit(sampleInput(func(in *Input) {
		in.Event.Phone = "13800000000"
		in.Event.ExplicitIntent = true
		in.Event.Purpose = PurposeSalesInquiry
	}))
	if withPhone.PhoneMarketing || withPhone.SMSMarketing {
		t.Fatalf("a phone on the message was treated as marketing consent: %+v", withPhone)
	}
}

// 生产代码若接受调用方指定的别的租户或别的来源，这条会失败。
func TestRejectsForgedTenantAndSource(t *testing.T) {
	tenant := Admit(sampleInput(func(in *Input) {
		in.Event.TargetTenantID = "tnt_other"
	}))
	if tenant.OK || tenant.Refusal != RefusalForgedTenant {
		t.Fatalf("forged tenant: %+v", tenant)
	}
	source := Admit(sampleInput(func(in *Input) {
		in.Event.Provider = "other_connector"
	}))
	if source.OK || source.Refusal != RefusalForgedSource {
		t.Fatalf("forged provider: %+v", source)
	}
	ns := Admit(sampleInput(func(in *Input) {
		in.Event.SubjectNS = "foreign.subject"
	}))
	if ns.OK || ns.Refusal != RefusalForgedSource {
		t.Fatalf("forged namespace: %+v", ns)
	}
}

// 生产代码若在重放或撤回时再造一条线索候选，这条会失败。
func TestReplayAndRetractDoNotCreateAnotherCandidate(t *testing.T) {
	first := Admit(sampleInput(func(in *Input) {
		in.Event.ExplicitIntent = true
		in.Event.Purpose = PurposeSalesInquiry
	}))
	if !first.CreateCandidate {
		t.Fatalf("first delivery: %+v", first)
	}
	replay := Admit(sampleInput(func(in *Input) {
		in.Event.ExplicitIntent = true
		in.Event.Purpose = PurposeSalesInquiry
		in.Prior = &Stored{InteractionID: "ix_1", CandidateID: "cand_1", TextSHA256: first.TextSHA256}
	}))
	if !replay.Idempotent || replay.CreateInteraction || replay.CreateCandidate || replay.InteractionID != "ix_1" || replay.CandidateID != "cand_1" {
		t.Fatalf("replay: %+v", replay)
	}
	retract := Admit(sampleInput(func(in *Input) {
		in.Event.Retracted = true
		in.Event.ExplicitIntent = true
		in.Event.Purpose = PurposeSalesInquiry
		in.Prior = &Stored{InteractionID: "ix_1", CandidateID: "cand_1", TextSHA256: first.TextSHA256}
	}))
	if !retract.Retract || retract.CreateCandidate || !retract.WithdrawCandidate || retract.CandidateID != "cand_1" {
		t.Fatalf("retract: %+v", retract)
	}
	again := Admit(sampleInput(func(in *Input) {
		in.Event.Retracted = true
		in.Prior = &Stored{InteractionID: "ix_1", CandidateID: "cand_1", TextSHA256: first.TextSHA256, Retracted: true}
	}))
	if again.CreateCandidate || again.CreateInteraction {
		t.Fatalf("retract replay created records: %+v", again)
	}
	edit := Admit(sampleInput(func(in *Input) {
		in.Event.Text = "改成要采购合同"
		in.Event.ExplicitIntent = true
		in.Event.Purpose = PurposeSalesInquiry
		in.Prior = &Stored{InteractionID: "ix_1", CandidateID: "cand_1", TextSHA256: first.TextSHA256}
	}))
	if edit.CreateInteraction || edit.CreateCandidate || !edit.Update || edit.CandidateID != "cand_1" || edit.TextSHA256 == first.TextSHA256 {
		t.Fatalf("edit created a second candidate: %+v", edit)
	}
}

// 生产代码若因高分自动触达，或在矩阵机器人占线时再让获客机器人回复，这条会失败。
func TestHighScoreDoesNotReachAndTwoBotsDoNotBothReply(t *testing.T) {
	scored := Admit(sampleInput(func(in *Input) {
		in.Event.Score = 99
		in.Event.ExplicitIntent = true
		in.Event.Purpose = PurposeSalesInquiry
	}))
	if scored.AutoReach || scored.ReplyAllowed || scored.HumanProcessed {
		t.Fatalf("score armed outreach: %+v", scored)
	}
	conflict := Admit(sampleInput(func(in *Input) {
		in.Grant.Capabilities = []string{CapCommentRead, CapReply}
		in.WantReply = true
		in.Floor = SpeakerMatrix
	}))
	if conflict.ReplyAllowed || conflict.ReplyRefusal != RefusalSpeaker || conflict.Delivered {
		t.Fatalf("second bot was allowed to speak: %+v", conflict)
	}
	human := Admit(sampleInput(func(in *Input) {
		in.Grant.Capabilities = []string{CapCommentRead, CapReply}
		in.WantReply = true
		in.Floor = SpeakerHuman
	}))
	if human.ReplyAllowed || human.ReplyRefusal != RefusalSpeaker {
		t.Fatalf("bot replied during human takeover: %+v", human)
	}
	open := Admit(sampleInput(func(in *Input) {
		in.Grant.Capabilities = []string{CapCommentRead, CapReply}
		in.WantReply = true
	}))
	if !open.ReplyAllowed || open.AutoReach || open.Delivered || open.ReplyRefusal != "" {
		t.Fatalf("single authorized reply draft: %+v", open)
	}
}

// 生产代码若在权限撤销后让旧事件恢复读取，这条会失败。
func TestRevokedGrantRejectsReplay(t *testing.T) {
	got := Admit(sampleInput(func(in *Input) {
		in.Grant.Revoked = true
		in.Event.ExplicitIntent = true
		in.Event.Purpose = PurposeSalesInquiry
		in.Prior = &Stored{InteractionID: "ix_1", CandidateID: "cand_1", TextSHA256: "abc"}
	}))
	if got.OK || got.Refusal != RefusalRevoked || got.CreateCandidate || got.Idempotent {
		t.Fatalf("revoked replay: %+v", got)
	}
}

// 生产代码若把客户全文写进日志或路径，这条会失败。
func TestAuditLineAndPathOmitCustomerText(t *testing.T) {
	const secret = "客户想买一百台并且留下了私信全文"
	line := AuditLine("tnt_a", "connector", "acct_1", "evt_1", secret)
	if strings.Contains(line, secret) || strings.Contains(line, "一百台") {
		t.Fatalf("audit line leaked text: %s", line)
	}
	path, err := InteractionPath("ix_1", secret)
	if err == nil || strings.Contains(path, secret) {
		t.Fatalf("path accepted customer text: %q %v", path, err)
	}
	clean, err := InteractionPath("ix_1", "")
	if err != nil || clean != "/channel-interactions/ix_1" {
		t.Fatalf("id path: %q %v", clean, err)
	}
}

// 生产代码若把某个平台标成已验证或已全量支持，这条会失败。
func TestCapabilityStaysUnverified(t *testing.T) {
	cap := Capability()
	if cap.Verification != "unverified" || len(cap.LiveProviders) != 0 || cap.PublishImpliesMessageRead {
		t.Fatalf("capability overclaimed: %+v", cap)
	}
	got := Admit(sampleInput(func(in *Input) {
		in.Grant.Provider = "douyin"
		in.Event.Provider = "douyin"
		in.Grant.CredentialClaim = "live"
	}))
	if got.Verification != "unverified" || len(got.LiveProviders) != 0 {
		t.Fatalf("named provider was marked live: %+v", got)
	}
}

func sampleInput(mutate func(*Input)) Input {
	in := Input{
		Grant: Grant{
			TenantID:     "tnt_a",
			Provider:     "connector",
			AccountID:    "acct_1",
			AppID:        "app_auth",
			SubjectNS:    "connector.subject",
			MessageNS:    "connector.message",
			PostNS:       "connector.post",
			Capabilities: []string{CapCommentRead},
		},
		Event: Event{
			TargetTenantID: "tnt_a",
			Provider:       "connector",
			AccountID:      "acct_1",
			AppID:          "app_auth",
			SubjectNS:      "connector.subject",
			MessageNS:      "connector.message",
			PostNS:         "connector.post",
			EventID:        "evt_1",
			Kind:           KindComment,
			SubjectID:      "sub_1",
			Nickname:       "小王",
			Text:           "这个多少钱",
			Purpose:        PurposeGeneralQA,
		},
	}
	if mutate != nil {
		mutate(&in)
	}
	return in
}
