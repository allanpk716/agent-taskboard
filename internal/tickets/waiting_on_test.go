package tickets

import (
	"testing"
	"time"
)

func TestWaitingOnOfFeedbackGuestLinkTurn(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.Local)
	ticket := feedbackFixture(Author{MemberID: 7, MemberName: "李工"}, []Event{
		{Seq: 3, Type: "reply", Actor: Author{MemberID: 7, AgentID: 9}},
	})
	links := []GuestLinkInfo{{CanEditBrief: true, ExpiresAt: now.Add(time.Hour)}}

	got := WaitingOnOf(ticket, links, now)
	if got.Party != "guest" || got.SinceSeq != 3 {
		t.Fatalf("有有效链接时轮次 = %+v, 想 guest/3", got)
	}
}

func TestWaitingOnOfFeedbackGuestLinkOtherMemberReply(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.Local)
	ticket := feedbackFixture(Author{MemberID: 7, MemberName: "李工"}, []Event{
		{Seq: 3, Type: "reply", Actor: Author{MemberID: 8, MemberName: "王工"}},
	})
	links := []GuestLinkInfo{{ExpiresAt: now.Add(time.Hour)}}
	if got := WaitingOnOf(ticket, links, now); got.Party != "guest" || got.SinceSeq != 3 {
		t.Fatalf("有有效链接时其他成员回复后轮次 = %+v, 想 guest/3", got)
	}
}

func TestWaitingOnOfFeedbackNoGuestLinkTurn(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.Local)
	assignee := Author{MemberID: 7, MemberName: "李工"}
	other := Author{MemberID: 8, MemberName: "王工"}
	agent := assignee
	agent.AgentID = 9
	agent.AgentName = "笔记本"
	personal := assignee
	guest := Author{LinkID: 1, AuthorName: "张三", LinkNote: "检验科-张三"}

	tests := []struct {
		name   string
		events []Event
		want   WaitingOn
	}{
		{
			name:   "指派人的 Agent 回复",
			events: []Event{{Seq: 3, Type: "reply", Actor: agent}},
			want:   WaitingOn{Party: "assignee", MemberID: 7, Member: "李工", Why: WhyAgentReplied, SinceSeq: 3},
		},
		{
			name: "指派人本人回复沿用上一条访客行动的原因",
			events: []Event{
				{Seq: 3, Type: "reply", Actor: guest},
				{Seq: 5, Type: "reply", Actor: personal},
			},
			want: WaitingOn{Party: "assignee", MemberID: 7, Member: "李工", Why: WhyGuestReplied, SinceSeq: 5},
		},
		{
			name: "其他成员回复不参与推导",
			events: []Event{
				{Seq: 3, Type: "reply", Actor: agent},
				{Seq: 5, Type: "reply", Actor: other},
			},
			want: WaitingOn{Party: "assignee", MemberID: 7, Member: "李工", Why: WhyAgentReplied, SinceSeq: 3},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WaitingOnOf(feedbackFixture(assignee, tt.events), nil, now)
			if got.Party != tt.want.Party || got.MemberID != tt.want.MemberID || got.Member != tt.want.Member ||
				got.Why != tt.want.Why || got.SinceSeq != tt.want.SinceSeq {
				t.Fatalf("轮次 = %+v, 想 %+v", got, tt.want)
			}
		})
	}
}

func TestWaitingOnOfFeedbackGuestLinkExpiryRecomputes(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.Local)
	assignee := Author{MemberID: 7, MemberName: "李工"}
	ticket := feedbackFixture(assignee, []Event{
		{Seq: 3, Type: "reply", Actor: Author{LinkID: 1, AuthorName: "张三", LinkNote: "检验科-张三"}},
		{Seq: 5, Type: "reply", Actor: assignee},
	})
	links := []GuestLinkInfo{{ExpiresAt: now.Add(time.Hour)}}

	if got := WaitingOnOf(ticket, links, now); got.Party != "guest" {
		t.Fatalf("链接有效时轮次 = %+v, 想 guest", got)
	}
	got := WaitingOnOf(ticket, links, now.Add(2*time.Hour))
	if got.Party != "assignee" || got.MemberID != assignee.MemberID || got.Why != WhyGuestReplied || got.SinceSeq != 5 {
		t.Fatalf("链接过期后轮次 = %+v, 想 李工/guest_replied/5", got)
	}
}

// 所属访客已停用视同链接失效：需要访客补全说明的工单回到指派人；
// 成员回复后的反馈工单不再轮到访客，理由沿用链接过期后的规则。
func TestWaitingOnOfGuestDisabledTreatedAsInvalidLink(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.Local)
	assignee := Author{MemberID: 7, MemberName: "李工"}
	disabledLink := func(canEdit bool) []GuestLinkInfo {
		return []GuestLinkInfo{{
			CanEditBrief: canEdit, GuestDisabled: true, ExpiresAt: now.Add(time.Hour),
		}}
	}
	tests := []struct {
		name   string
		events []Event
		links  []GuestLinkInfo
		want   WaitingOn
	}{
		{
			name:  "访客未补全说明而访客已停用",
			links: disabledLink(true),
			want:  WaitingOn{Party: "assignee", MemberID: 7, Member: "李工", Why: WhyGuestNotCompleted},
		},
		{
			name: "成员回复后访客已停用",
			events: []Event{
				{Seq: 3, Type: "reply", Actor: Author{LinkID: 1, AuthorName: "张三", GuestName: "检验科-张三"}},
				{Seq: 5, Type: "reply", Actor: assignee},
			},
			links: disabledLink(false),
			want:  WaitingOn{Party: "assignee", MemberID: 7, Member: "李工", Why: WhyGuestReplied, SinceSeq: 5},
		},
		{
			name: "同一访客的另一条有效链接仍可轮到访客",
			events: []Event{
				{Seq: 3, Type: "reply", Actor: assignee},
			},
			links: append(disabledLink(false), GuestLinkInfo{ExpiresAt: now.Add(time.Hour)}),
			want:  WaitingOn{Party: "guest", Text: "访客", SinceSeq: 3},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WaitingOnOf(feedbackFixture(assignee, tt.events), tt.links, now)
			if got.Party != tt.want.Party || got.MemberID != tt.want.MemberID ||
				got.Member != tt.want.Member || got.Why != tt.want.Why || got.SinceSeq != tt.want.SinceSeq {
				t.Fatalf("轮次 = %+v, 想 %+v", got, tt.want)
			}
		})
	}
}

func feedbackFixture(assignee Author, events []Event) Full {
	return Full{
		Ticket: Ticket{
			Kind: "report", Status: "open", Title: "质控结果偏高",
			AssigneeID: assignee.MemberID, AssigneeName: assignee.MemberName,
		},
		Timeline: events,
	}
}
