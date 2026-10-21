package server_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test同一幂等键重放时可见性被收回返回404并记录被拒(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	s.CreateMember("创建者", "secret-pass")
	s.CreateMember("指派人", "secret-pass")
	s.CreateMember("旁观者", "secret-pass")
	s.CreateMember("被移除者", "secret-pass")
	admin := s.Login("管理员", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	assignee := s.Login("指派人", "secret-pass")
	outsider := s.Login("旁观者", "secret-pass")

	var created createTicketResponse
	res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "幂等重放前的可见工单", "labels": []string{"机型:UD-2300"}, "assignee": "指派人",
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}
	s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+created.Number+"/collaborators",
		map[string]any{"member": "旁观者"}, nil)
	s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+created.Number+"/collaborators",
		map[string]any{"member": "被移除者"}, nil)

	removeKey := map[string]string{"Idempotency-Key": "remove-visibility-replay"}
	first := s.DoWithHeaders(t, assignee, http.MethodDelete,
		"/api/v1/tickets/"+created.Number+"/collaborators/5", removeKey, nil, nil)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("首次移出协作者状态码 = %d", first.StatusCode)
	}
	if res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+created.Number+"/assign",
		map[string]any{"assignee": "创建者"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("收回可见范围状态码 = %d", res.StatusCode)
	}
	replay := s.DoWithHeaders(t, assignee, http.MethodDelete,
		"/api/v1/tickets/"+created.Number+"/collaborators/5", removeKey, nil, nil)
	if replay.StatusCode != http.StatusNotFound {
		t.Fatalf("可见性收回后的幂等重放状态码 = %d, 想 404", replay.StatusCode)
	}

	leaveKey := map[string]string{"Idempotency-Key": "leave-visibility-replay"}
	first = s.DoWithHeaders(t, outsider, http.MethodPost,
		"/api/v1/tickets/"+created.Number+"/collaborators/leave", leaveKey, nil, nil)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("首次退出协作者状态码 = %d", first.StatusCode)
	}
	replay = s.DoWithHeaders(t, outsider, http.MethodPost,
		"/api/v1/tickets/"+created.Number+"/collaborators/leave", leaveKey, nil, nil)
	if replay.StatusCode != http.StatusNotFound {
		t.Fatalf("退出后的幂等重放状态码 = %d, 想 404", replay.StatusCode)
	}

	records := readAccessRecords(t, s, admin, created.Number)
	denied := 0
	for _, record := range records.Records {
		if record.Action == "denied" {
			denied++
		}
	}
	if denied != 2 {
		t.Fatalf("被拒记录有 %d 条, 想 2 条：%+v", denied, records.Records)
	}
}

func Test不可见成员设置标签返回工单404并记录被拒(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	s.CreateMember("创建者", "secret-pass")
	s.CreateMember("旁观者", "secret-pass")
	admin := s.Login("管理员", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	outsider := s.Login("旁观者", "secret-pass")
	var created createTicketResponse
	res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "不可见工单的标签", "labels": []string{"机型:UD-2300"},
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}
	s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+created.Number+"/collaborators",
		map[string]any{"member": "旁观者"}, nil)
	number := created.Number

	started := make(chan struct{})
	release := make(chan struct{})
	reqBody := &gatedJSONReader{
		body:    []byte(`{"add":["机型:UD-2300"]}`),
		started: started,
		release: release,
	}
	req, err := http.NewRequest(http.MethodPost, s.URL()+"/api/v1/tickets/"+number+"/labels", reqBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	type requestResult struct {
		response *http.Response
		err      error
	}
	done := make(chan requestResult, 1)
	go func() {
		response, err := outsider.Do(req)
		done <- requestResult{response: response, err: err}
	}()
	<-started
	if res := s.Do(t, creator, http.MethodDelete, "/api/v1/tickets/"+number+"/collaborators/3", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("移出协作者状态码 = %d", res.StatusCode)
	}
	close(release)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	defer got.response.Body.Close()
	if got.response.StatusCode != http.StatusNotFound {
		t.Fatalf("写入时不可见的标签请求状态码 = %d, 想 404", got.response.StatusCode)
	}

	records := readAccessRecords(t, s, admin, number)
	denied := 0
	for _, record := range records.Records {
		if record.Action == "denied" {
			denied++
		}
	}
	if denied != 1 {
		t.Fatalf("被拒记录有 %d 条, 想 1 条：%+v", denied, records.Records)
	}
}

type gatedJSONReader struct {
	body    []byte
	started chan struct{}
	release chan struct{}
	read    bool
}

func (r *gatedJSONReader) Read(p []byte) (int, error) {
	if !r.read {
		r.read = true
		close(r.started)
		<-r.release
	}
	if len(r.body) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.body)
	r.body = r.body[n:]
	return n, nil
}
