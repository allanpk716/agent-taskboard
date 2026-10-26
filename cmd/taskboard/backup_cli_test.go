// taskboard backup / restore 的 CLI 测试（#43）：临时目录充当 data_dir 和备份
// 目标，走真实二进制。备份文件命名正确并通过完整性校验；恢复后工单数一致、
// 附件能打开。
package main_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	_ "modernc.org/sqlite"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cliFixture 是一个可以反复启停的服务环境（真实二进制 + 临时目录）。
type cliFixture struct {
	t          *testing.T
	bin        string
	dir        string
	dataDir    string
	target     string
	configPath string
	port       int
}

// startCLIEnvironment 建目录写配置并创建管理员，返回环境（服务未启动）。
func startCLIEnvironment(t *testing.T) *cliFixture {
	t.Helper()
	bin := buildBinary(t)
	dir := t.TempDir()
	f := &cliFixture{
		t:          t,
		bin:        bin,
		dir:        dir,
		dataDir:    filepath.Join(dir, "data"),
		target:     filepath.Join(dir, "nas-backup"),
		configPath: filepath.Join(dir, "config.toml"),
		port:       freePort(t),
	}
	webDir := filepath.Join(dir, "web")
	if err := os.MkdirAll(webDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!doctype html><title>ok</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.target, 0o755); err != nil {
		t.Fatal(err)
	}
	configToml := fmt.Sprintf("data_dir = '%s'\nport = %d\nweb_dir = '%s'\n[backup]\ntarget = '%s'\n",
		filepath.ToSlash(f.dataDir), f.port, filepath.ToSlash(webDir), filepath.ToSlash(f.target))
	if err := os.WriteFile(f.configPath, []byte(configToml), 0o644); err != nil {
		t.Fatal(err)
	}
	runTaskboard(t, bin, "create-admin", "-config", f.configPath, "-username", "admin", "-password", "secret-pass")
	return f
}

// serve 启动服务直到返回的 stop 被调用。
func (f *cliFixture) serve() (base string, stop func()) {
	f.t.Helper()
	serve := exec.Command(f.bin, "serve", "-config", f.configPath)
	serve.Stdout = os.Stdout
	serve.Stderr = os.Stderr
	if err := serve.Start(); err != nil {
		f.t.Fatal(err)
	}
	base = fmt.Sprintf("http://127.0.0.1:%d", f.port)
	waitReady(f.t, base)
	return base, func() {
		serve.Process.Kill()
		serve.Wait()
	}
}

// cookieClient 是带会话 cookie 的客户端（登录后的成员本人）。
func (f *cliFixture) cookieClient(base string) *http.Client {
	f.t.Helper()
	res, err := postLogin(base, "admin", "secret-pass")
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == "tb_session" && c.Value != "" {
			return &http.Client{Transport: fixedCookieTransport{c.Value}}
		}
	}
	f.t.Fatal("登录后没有会话 cookie")
	return nil
}

type fixedCookieTransport struct{ cookie string }

func (t fixedCookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Cookie", "tb_session="+t.cookie)
	return http.DefaultTransport.RoundTrip(clone)
}

// createTicket 建一张工单，返回编号。
func (f *cliFixture) createTicket(c *http.Client, base, title string) string {
	f.t.Helper()
	body := `{"kind":"task","title":"` + title + `","brief_md":"说明","labels":["机型:UD-2300"]}`
	res, err := c.Post(base+"/api/v1/tickets", "application/json", strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		out, _ := io.ReadAll(res.Body)
		f.t.Fatalf("建单 %s 失败：%d %s", title, res.StatusCode, out)
	}
	var created struct {
		Number string `json:"number"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		f.t.Fatal(err)
	}
	return created.Number
}

// uploadAndReplyAttachment 上传一个附件并在回复里引用它（挂到工单上）。
func (f *cliFixture) uploadAndReplyAttachment(c *http.Client, base, number, filename string, content []byte) {
	f.t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		f.t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		f.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/tickets/"+number+"/attachments", &body)
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := c.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		out, _ := io.ReadAll(res.Body)
		f.t.Fatalf("上传附件失败：%d %s", res.StatusCode, out)
	}
	var uploaded struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&uploaded); err != nil {
		f.t.Fatal(err)
	}
	reply := `{"body_md":"看附件","attachment_ids":["` + uploaded.ID + `"]}`
	res2, err := c.Post(base+"/api/v1/tickets/"+number+"/replies", "application/json", strings.NewReader(reply))
	if err != nil {
		f.t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusCreated {
		out, _ := io.ReadAll(res2.Body)
		f.t.Fatalf("回复挂附件失败：%d %s", res2.StatusCode, out)
	}
}

// taskboard 命令行备份：文件按 taskboard-YYYYMMDD-HHMMSS-<kind>.db 命名到目标，
// 内容是库的真实快照，通过完整性校验（服务运行中在线备份）。
func Test命令行备份命名正确并通过完整性校验(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译并启动真实二进制")
	}
	f := startCLIEnvironment(t)
	base, stop := f.serve()
	c := f.cookieClient(base)
	f.createTicket(c, base, "备份测试工单")
	stop() // 备份本身支持在线进行，这里先停掉避免与后续测试抢端口

	out := runTaskboard(t, f.bin, "backup", "-config", f.configPath)
	matches := globBackupFiles(t, f.target, "daily")
	if len(matches) != 1 {
		t.Fatalf("目标里应有一个每日备份文件：%v（输出 %s）", matches, out)
	}
	if !strings.Contains(out, matches[0]) {
		t.Fatalf("backup 输出应包含文件名 %s：%s", matches[0], out)
	}
	// 副本是合法的 SQLite 快照，完整性校验通过。
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(f.target, matches[0]))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var check string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		t.Fatalf("副本完整性校验 = %q (err %v)", check, err)
	}
	var tickets int
	if err := db.QueryRow("SELECT COUNT(*) FROM tickets").Scan(&tickets); err != nil || tickets != 1 {
		t.Fatalf("副本里应有 1 张工单，得到 %d (err %v)", tickets, err)
	}
}

func Test更新前备份输出最后一行是文件名(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译真实二进制")
	}
	f := startCLIEnvironment(t)
	out := runTaskboard(t, f.bin, "backup", "-preupdate", "-config", f.configPath)
	lines := strings.Split(strings.TrimRight(out, "\r\n"), "\n")
	filename := strings.TrimRight(lines[len(lines)-1], "\r")
	if filename == "" || strings.ContainsAny(filename, `/\`) {
		t.Fatalf("backup -preupdate 最后一行应是不带路径的文件名：%q（输出 %s）", filename, out)
	}
	matches := globBackupFiles(t, f.target, "preupdate")
	if len(matches) != 1 || matches[0] != filename {
		t.Fatalf("更新前备份文件 = %v，想 [%s]（输出 %s）", matches, filename, out)
	}
}

// 恢复演练（黑盒全流程）：数据写入 → 备份 → 抹掉 → 恢复 → 工单数一致、附件能打开。
func Test命令行恢复后工单数一致附件能打开(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过：需要编译并启动真实二进制")
	}
	f := startCLIEnvironment(t)
	base, stop := f.serve()
	c := f.cookieClient(base)
	f.createTicket(c, base, "第一张")
	number2 := f.createTicket(c, base, "第二张")
	attContent := []byte("附件内容：日志一页")
	f.uploadAndReplyAttachment(c, base, number2, "运行日志.txt", attContent)

	// 服务运行中做在线备份（VACUUM INTO 不需要停服务）。
	runTaskboard(t, f.bin, "backup", "-config", f.configPath)
	backup := globBackupFiles(t, f.target, "daily")[0]
	backupPath := filepath.Join(f.target, backup)

	// 服务还在跑时恢复被拒绝。
	if out, err := exec.Command(f.bin, "restore", backupPath, "-config", f.configPath).CombinedOutput(); err == nil {
		t.Fatalf("服务运行中应拒绝恢复：%s", out)
	}

	// 停服务，“硬盘坏了”：库写花、附件丢失，然后从备份恢复。
	stop()
	if err := os.WriteFile(filepath.Join(f.dataDir, "taskboard.db"), []byte("库文件损坏了"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		os.Remove(filepath.Join(f.dataDir, "taskboard.db"+suffix))
	}
	atts, err := os.ReadDir(filepath.Join(f.dataDir, "attachments"))
	if err == nil {
		for _, e := range atts {
			os.Remove(filepath.Join(f.dataDir, "attachments", e.Name()))
		}
	}
	out := runTaskboard(t, f.bin, "restore", backupPath, "-config", f.configPath)
	if !strings.Contains(out, "恢复完成") {
		t.Fatalf("restore 输出应说明恢复完成：%s", out)
	}

	// 另存了一份当前库。
	saved, err := filepath.Glob(filepath.Join(f.dataDir, "taskboard.db.pre-restore-*"))
	if err != nil || len(saved) != 1 {
		t.Fatalf("应另存一份当前库：%v (err %v)", saved, err)
	}

	// 重启服务：工单数一致（1、2 都在，3 不存在），附件内容不变。
	base, stop = f.serve()
	defer stop()
	c = f.cookieClient(base)
	for _, ref := range []string{"1", "2"} {
		res, err := c.Get(base + "/api/v1/tickets/" + ref)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("恢复后工单 %s 应存在：%d %s", ref, res.StatusCode, body)
		}
	}
	res, err := c.Get(base + "/api/v1/tickets/3")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("工单 3 应不存在（工单数一致），状态码 %d", res.StatusCode)
	}

	// 附件能打开，内容不变。
	res, err = c.Get(base + "/api/v1/tickets/2")
	if err != nil {
		t.Fatal(err)
	}
	var ticket struct {
		Attachments []struct {
			Filename string `json:"filename"`
			URL      string `json:"url"`
		} `json:"attachments"`
	}
	json.NewDecoder(res.Body).Decode(&ticket)
	res.Body.Close()
	if len(ticket.Attachments) != 1 {
		t.Fatalf("恢复后工单 2 应有 1 个附件：%+v", ticket.Attachments)
	}
	res, err = c.Get(base + ticket.Attachments[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(got) != string(attContent) {
		t.Fatalf("附件内容应与备份前一致：%q", got)
	}
}

// globBackupFiles 列出目标里某类别的备份文件名（不含目录，新→旧）。
func globBackupFiles(t *testing.T, dir, kind string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "taskboard-*-"+kind+".db"))
	if err != nil {
		t.Fatal(err)
	}
	matches := make([]string, 0, len(paths))
	for _, p := range paths {
		matches = append(matches, filepath.Base(p))
	}
	// 新→旧。
	for i, j := 0, len(matches)-1; i < j; i, j = i+1, j-1 {
		matches[i], matches[j] = matches[j], matches[i]
	}
	return matches
}
