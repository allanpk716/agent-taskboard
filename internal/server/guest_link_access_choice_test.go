package server_test

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 未认领访客可以指定一条已认领老链接作为访问码来源；这条链接的设备与
// 访问码整体转给访客，其余所选链接的旧凭证作废。
func Test未认领访客批量归属可沿用所选老链接访问码(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)
	targetTicket := mustCreateTicket(t, s, web, "未认领访客的已有工单")
	mustCreateGuestLink(t, s, web, targetTicket,
		map[string]any{"note": "访客链接", "new_guest_name": "检验科-张三"})
	guestID := listGuestLinks(t, s, web, targetTicket).Links[0].GuestID

	sourceTicket := mustCreateTicket(t, s, web, "访问码来源老工单")
	source := s.CreateLegacyGuestLink(t, web, sourceTicket,
		map[string]any{"note": "检验科-张三"})
	sourceClaim := claimGuestLink(t, s,
		guestKeyOf(t, guestLinkResponse{ID: source.ID, URL: source.URL}), "张三")
	otherClaimedTicket := mustCreateTicket(t, s, web, "另一条已认领老工单")
	otherClaimed := s.CreateLegacyGuestLink(t, web, otherClaimedTicket,
		map[string]any{"note": "检验科-张三旧联系"})
	otherClaim := claimGuestLink(t, s,
		guestKeyOf(t, guestLinkResponse{ID: otherClaimed.ID, URL: otherClaimed.URL}), "老张三")
	unclaimedTicket := mustCreateTicket(t, s, web, "未认领老工单")
	unclaimed := s.CreateLegacyGuestLink(t, web, unclaimedTicket,
		map[string]any{"note": "检验科-张三"})

	var preview batchAssignPreviewResponse
	previewPath := "/api/v1/guests/" + itoa(guestID) + "/assign-links/preview?link_ids=" +
		itoa(source.ID) + "," + itoa(otherClaimed.ID) + "," + itoa(unclaimed.ID) +
		"&source_link_id=" + itoa(source.ID)
	if res := s.Do(t, web, http.MethodGet, previewPath, nil, &preview); res.StatusCode != http.StatusOK {
		t.Fatalf("沿用访问码预览状态码 = %d，想 200", res.StatusCode)
	}
	if preview.AccessCodeAction != "reuse_source_link" || preview.SourceLinkID != source.ID ||
		preview.SourceDeviceCount != 1 || preview.InvalidatedDeviceCount != 1 {
		t.Fatalf("沿用访问码预览 = %+v，想来源设备转给访客、其余设备作废", preview)
	}

	res, result := batchAssignGuestLinks(t, s, agent, guestID, map[string]any{
		"link_ids":    []int64{unclaimed.ID, otherClaimed.ID, source.ID},
		"access_code": map[string]any{"source_link_id": source.ID},
	})
	if res.StatusCode != http.StatusOK || result.AccessCodeAction != "reuse_source_link" ||
		result.AccessCode != "" || result.LinkCount != 3 {
		t.Fatalf("成员 Agent 沿用访问码批量归属 = %d/%+v，想 200/reuse_source_link", res.StatusCode, result)
	}

	for _, ticket := range []string{sourceTicket, otherClaimedTicket, unclaimedTicket} {
		if res := s.Do(t, s.AgentClient(sourceClaim.DeviceToken), http.MethodGet,
			"/api/v1/tickets/"+ticket, nil, nil); res.StatusCode != http.StatusOK {
			t.Fatalf("来源链接设备读取 %s 状态码 = %d，想 200", ticket, res.StatusCode)
		}
	}
	if res := s.Do(t, s.AgentClient(otherClaim.DeviceToken), http.MethodGet,
		"/api/v1/tickets/"+otherClaimedTicket, nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("非来源已认领链接设备状态码 = %d，想 401", res.StatusCode)
	}
	var entered claimResult
	if res := s.Do(t, s.AgentClient(guestKeyOf(t, guestLinkResponse{ID: unclaimed.ID, URL: unclaimed.URL})),
		http.MethodPost, "/api/v1/guest-links/enter",
		map[string]string{"access_code": sourceClaim.AccessCode}, &entered); res.StatusCode != http.StatusCreated {
		t.Fatalf("沿用访问码进入全部链接状态码 = %d，想 201", res.StatusCode)
	}
	if res := s.Do(t, s.AgentClient(guestKeyOf(t, guestLinkResponse{ID: unclaimed.ID, URL: unclaimed.URL})),
		http.MethodPost, "/api/v1/guest-links/enter",
		map[string]string{"access_code": otherClaim.AccessCode}, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("非来源旧访问码状态码 = %d，想 401", res.StatusCode)
	}
	var detail struct {
		Guest struct {
			ClaimName string `json:"claim_name"`
		} `json:"guest"`
	}
	if res := s.Do(t, web, http.MethodGet, "/api/v1/guests/"+itoa(guestID), nil, &detail); res.StatusCode != http.StatusOK ||
		detail.Guest.ClaimName != "张三" {
		t.Fatalf("沿用后访客称呼 = %d/%q，想来源链接称呼 张三", res.StatusCode, detail.Guest.ClaimName)
	}
}

// 未认领访客也可以生成新访问码；新码只在提交响应出现一次，旧码与设备全作废。
// 生成新码只接受网页成员会话，成员 Agent 会被拒绝。
func Test未认领访客批量归属可生成只显示一次的新访问码(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)
	targetTicket := mustCreateTicket(t, s, web, "生成新码访客的已有工单")
	mustCreateGuestLink(t, s, web, targetTicket,
		map[string]any{"note": "访客链接", "new_guest_name": "检验科-张三"})
	guestID := listGuestLinks(t, s, web, targetTicket).Links[0].GuestID
	claimedTicket := mustCreateTicket(t, s, web, "生成新码前的老工单")
	claimed := s.CreateLegacyGuestLink(t, web, claimedTicket,
		map[string]any{"note": "检验科-张三"})
	oldClaim := claimGuestLink(t, s,
		guestKeyOf(t, guestLinkResponse{ID: claimed.ID, URL: claimed.URL}), "老张三")
	unclaimedTicket := mustCreateTicket(t, s, web, "生成新码的另一张老工单")
	unclaimed := s.CreateLegacyGuestLink(t, web, unclaimedTicket,
		map[string]any{"note": "检验科-张三"})

	var preview batchAssignPreviewResponse
	previewPath := "/api/v1/guests/" + itoa(guestID) + "/assign-links/preview?link_ids=" +
		itoa(claimed.ID) + "," + itoa(unclaimed.ID) + "&access_code=generate"
	if res := s.Do(t, web, http.MethodGet, previewPath, nil, &preview); res.StatusCode != http.StatusOK {
		t.Fatalf("生成新码预览状态码 = %d，想 200", res.StatusCode)
	}
	if preview.AccessCodeAction != "generate" || preview.SourceLinkID != 0 ||
		preview.SourceDeviceCount != 0 || preview.InvalidatedDeviceCount != 1 {
		t.Fatalf("生成新码预览 = %+v，想旧设备全部作废且不出现新码", preview)
	}

	res, _ := batchAssignGuestLinks(t, s, agent, guestID, map[string]any{
		"link_ids": []int64{claimed.ID, unclaimed.ID}, "access_code": "generate",
	})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("成员 Agent 生成新码状态码 = %d，想 403", res.StatusCode)
	}
	var forbidden errorBody
	tbtest.DecodeJSON(t, res, &forbidden)
	if forbidden.Error.Code != "web_session_required" {
		t.Fatalf("成员 Agent 生成新码错误码 = %q，想 web_session_required", forbidden.Error.Code)
	}

	res, result := batchAssignGuestLinks(t, s, web, guestID, map[string]any{
		"link_ids": []int64{claimed.ID, unclaimed.ID}, "access_code": "generate",
	})
	if res.StatusCode != http.StatusOK || result.AccessCodeAction != "generate" ||
		!regexp.MustCompile(`^\d{6}$`).MatchString(result.AccessCode) {
		t.Fatalf("生成新码批量归属 = %d/%+v，想 200 且一次性返回 6 位新码", res.StatusCode, result)
	}
	var detail struct {
		Guest struct {
			ClaimName   string `json:"claim_name"`
			DeviceCount int    `json:"device_count"`
		} `json:"guest"`
	}
	if res := s.Do(t, web, http.MethodGet, "/api/v1/guests/"+itoa(guestID), nil, &detail); res.StatusCode != http.StatusOK ||
		detail.Guest.ClaimName != "检验科-张三" || detail.Guest.DeviceCount != 0 {
		t.Fatalf("生成新码后的访客认领状态 = %+v", detail.Guest)
	}
	if res := s.Do(t, s.AgentClient(oldClaim.DeviceToken), http.MethodGet,
		"/api/v1/tickets/"+claimedTicket, nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("生成新码后旧设备状态码 = %d，想 401", res.StatusCode)
	}
	if res := s.Do(t, s.AgentClient(guestKeyOf(t, guestLinkResponse{ID: claimed.ID, URL: claimed.URL})),
		http.MethodPost, "/api/v1/guest-links/enter",
		map[string]string{"access_code": oldClaim.AccessCode}, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("生成新码后旧访问码状态码 = %d，想 401", res.StatusCode)
	}
	for _, link := range []guestLinkResponse{
		{ID: claimed.ID, URL: claimed.URL}, {ID: unclaimed.ID, URL: unclaimed.URL},
	} {
		var entered claimResult
		if res := s.Do(t, s.AgentClient(guestKeyOf(t, link)), http.MethodPost,
			"/api/v1/guest-links/enter",
			map[string]string{"access_code": result.AccessCode}, &entered); res.StatusCode != http.StatusCreated {
			t.Fatalf("新访问码进入链接 %d 状态码 = %d，想 201", link.ID, res.StatusCode)
		}
	}
	for _, path := range []string{
		"/api/v1/guests/" + itoa(guestID),
		"/api/v1/guests/" + itoa(guestID) + "/reset-access-code/preview",
		"/api/v1/guest-links/unassigned",
	} {
		res := s.Do(t, web, http.MethodGet, path, nil, nil)
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("读取 %s 响应失败：%v", path, err)
		}
		if strings.Contains(string(body), result.AccessCode) {
			t.Fatalf("新访问码不应出现在后续接口 %s 响应中", path)
		}
	}
}

// source_link_id 必须来自本批所选且已认领；缺少选项时仍要求成员先选择。
func Test未认领访客批量归属访问码选项校验(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	targetTicket := mustCreateTicket(t, s, web, "访问码校验访客的工单")
	mustCreateGuestLink(t, s, web, targetTicket,
		map[string]any{"note": "访客链接", "new_guest_name": "检验科-张三"})
	guestID := listGuestLinks(t, s, web, targetTicket).Links[0].GuestID
	claimedTicket := mustCreateTicket(t, s, web, "校验用已认领老工单")
	claimed := s.CreateLegacyGuestLink(t, web, claimedTicket,
		map[string]any{"note": "检验科-张三"})
	claimGuestLink(t, s, guestKeyOf(t, guestLinkResponse{ID: claimed.ID, URL: claimed.URL}), "张三")
	unclaimedTicket := mustCreateTicket(t, s, web, "校验用未认领老工单")
	unclaimed := s.CreateLegacyGuestLink(t, web, unclaimedTicket,
		map[string]any{"note": "检验科-张三"})
	outsideTicket := mustCreateTicket(t, s, web, "不在本批的老工单")
	outside := s.CreateLegacyGuestLink(t, web, outsideTicket,
		map[string]any{"note": "检验科-张三"})
	claimGuestLink(t, s, guestKeyOf(t, guestLinkResponse{ID: outside.ID, URL: outside.URL}), "张三")

	for name, tc := range map[string]struct {
		query string
		code  string
	}{
		"缺少选项":      {"", ""},
		"来源未认领":     {"&source_link_id=" + itoa(unclaimed.ID), "invalid_access_code_choice"},
		"来源不在所选链接中": {"&source_link_id=" + itoa(outside.ID), "invalid_access_code_choice"},
	} {
		res := s.Do(t, web, http.MethodGet,
			"/api/v1/guests/"+itoa(guestID)+"/assign-links/preview?link_ids="+
				itoa(claimed.ID)+","+itoa(unclaimed.ID)+tc.query, nil, nil)
		wantStatus := http.StatusUnprocessableEntity
		if name == "缺少选项" {
			wantStatus = http.StatusOK
		}
		if res.StatusCode != wantStatus {
			t.Fatalf("[%s] 预览状态码 = %d，想 %d", name, res.StatusCode, wantStatus)
		}
		var got errorBody
		if wantStatus == http.StatusOK {
			var preview batchAssignPreviewResponse
			tbtest.DecodeJSON(t, res, &preview)
			if preview.AccessCodeAction != "choice_required" {
				t.Fatalf("缺少选项预览 = %+v，想 choice_required", preview)
			}
			continue
		}
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != tc.code {
			t.Fatalf("[%s] 错误码 = %q，想 %q", name, got.Error.Code, tc.code)
		}
	}
	for name, tc := range map[string]struct {
		accessCode any
		code       string
	}{
		"缺少选项":      {nil, "access_code_choice_required"},
		"来源未认领":     {map[string]any{"source_link_id": unclaimed.ID}, "invalid_access_code_choice"},
		"来源不在所选链接中": {map[string]any{"source_link_id": outside.ID}, "invalid_access_code_choice"},
	} {
		body := map[string]any{"link_ids": []int64{claimed.ID, unclaimed.ID}}
		if tc.accessCode != nil {
			body["access_code"] = tc.accessCode
		}
		res, _ := batchAssignGuestLinks(t, s, web, guestID, body)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("[%s] 提交状态码 = %d，想 422", name, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != tc.code {
			t.Fatalf("[%s] 提交错误码 = %q，想 %q", name, got.Error.Code, tc.code)
		}
	}
}
