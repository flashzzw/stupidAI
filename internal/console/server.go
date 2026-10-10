package console

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"codex-netcap/internal/app"
	"codex-netcap/internal/runtime"
	"codex-netcap/web"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func deny(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func Handler(application *app.Application, host, token string, assets fs.FS) http.Handler {
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		if r.Host != host {
			deny(w, 403, "Host 不匹配")
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && origin != "http://"+host {
			deny(w, 403, "请求来源不匹配")
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				deny(w, 405, "不支持的方法")
				return
			}
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodPost {
			if origin != "http://"+host && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				deny(w, 403, "写入需要同源请求")
				return
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				deny(w, 415, "需要 JSON 请求")
				return
			}
			limit := int64(1 << 20)
			if r.URL.Path == "/api/capabilities/package/import/preview" || r.URL.Path == "/api/capabilities/package/import" {
				limit = 56 << 20 // A 40 MiB ZIP encoded as JSON base64, plus request metadata.
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		decode := func(out any) error {
			d := json.NewDecoder(r.Body)
			d.DisallowUnknownFields()
			if err := d.Decode(out); err != nil {
				return err
			}
			if err := d.Decode(new(any)); err != io.EOF {
				return fmt.Errorf("需要一个 JSON 对象")
			}
			return nil
		}
		if strings.HasPrefix(r.URL.Path, "/api/observations/") && r.Method == http.MethodPost {
			var delivery app.ObservationDelivery
			if err := decode(&delivery); err != nil {
				deny(w, 400, "观察投递格式无效")
				return
			}
			credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			receipt, err := application.IngestObservation(strings.TrimPrefix(r.URL.Path, "/api/observations/"), credential, delivery)
			if err != nil {
				var unauthorized *app.ObserverUnauthorized
				status := 400
				if errors.As(err, &unauthorized) {
					status = 401
				}
				deny(w, status, err.Error())
				return
			}
			writeJSON(w, 202, receipt)
			return
		}
		if r.URL.Path == "/api/login" && r.Method == http.MethodPost {
			var input struct {
				Token string `json:"token"`
			}
			if decode(&input) != nil || !equal(input.Token, token) {
				deny(w, 401, "访问令牌无效")
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "netcap_session", Value: token, Path: "/api", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			writeJSON(w, 200, map[string]bool{"ok": true})
			return
		}
		credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if cookie, err := r.Cookie("netcap_session"); err == nil && credential == "" {
			credential = cookie.Value
		}
		if !equal(credential, token) {
			deny(w, 401, "请连接本地工作台")
			return
		}
		switch {
		case r.URL.Path == "/api/capabilities/codex-hooks" && r.Method == http.MethodPost:
			var request *app.CapabilityHookInspectionRequest
			if decode(&request) != nil || request == nil {
				deny(w, 400, "插件 hook 定义读取请求格式无效")
				return
			}
			result, err := application.InspectCapabilityHooks(r.Context(), *request)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case (r.URL.Path == "/api/capabilities/package/export/preview" || r.URL.Path == "/api/capabilities/package/export") && r.Method == http.MethodPost:
			var request app.CapabilityPackageExportRequest
			if decode(&request) != nil {
				deny(w, 400, "资源包导出请求格式无效")
				return
			}
			if strings.HasSuffix(r.URL.Path, "/preview") {
				result, err := application.PreviewCapabilityPackageExport(r.Context(), request)
				if err != nil {
					deny(w, 409, err.Error())
					return
				}
				writeJSON(w, 200, result)
				return
			}
			data, err := application.ExportCapabilityPackage(r.Context(), request)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Disposition", `attachment; filename="capability-package.zip"`)
			w.Write(data)
		case r.URL.Path == "/api/capabilities/package/import/preview" && r.Method == http.MethodPost:
			var request app.CapabilityPackageImportRequest
			if decode(&request) != nil {
				deny(w, 400, "资源包导入预览格式无效")
				return
			}
			result, err := application.PreviewCapabilityPackageImport(r.Context(), request)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case r.URL.Path == "/api/capabilities/package/import" && r.Method == http.MethodPost:
			var command app.Command
			if decode(&command) != nil || command.Kind != "import_capability_package" {
				deny(w, 400, "资源包导入命令格式无效")
				return
			}
			result, err := application.Apply(command)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case r.URL.Path == "/api/capabilities" && r.Method == http.MethodGet:
			result, err := application.ReadCapabilities(r.Context())
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case r.URL.Path == "/api/capabilities/export" && r.Method == http.MethodGet:
			result, err := application.ExportCapabilityCatalog(r.Context())
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			w.Header().Set("Content-Disposition", `attachment; filename="capability-catalog.json"`)
			writeJSON(w, 200, result)
		case r.URL.Path == "/api/capabilities/preview" && r.Method == http.MethodPost:
			var request app.CapabilityChangeRequest
			if decode(&request) != nil {
				deny(w, 400, "能力变更预览格式无效")
				return
			}
			result, err := application.PreviewCapabilityChange(r.Context(), request)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)

		case r.URL.Path == "/api/execution-profiles/preview" && r.Method == http.MethodPost:
			var profile app.ExecutionProfile
			if decode(&profile) != nil {
				deny(w, 400, "执行配置预览格式无效")
				return
			}
			result, err := application.PreviewExecutionProfile(r.Context(), profile)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case r.URL.Path == "/api/execution-profiles/diagnose" && r.Method == http.MethodPost:
			var request struct{}
			if decode(&request) != nil {
				deny(w, 400, "诊断请求必须为空对象")
				return
			}
			result, err := application.DiagnoseExecutionProfiles(r.Context())
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case r.URL.Path == "/api/snapshot" && r.Method == http.MethodGet:
			writeJSON(w, 200, application.Snapshot())
		case r.URL.Path == "/api/workspace" && r.Method == http.MethodGet:
			writeJSON(w, 200, application.Workspace())
		case r.URL.Path == "/api/domain-events" && r.Method == http.MethodGet:
			writeJSON(w, 200, application.DomainEvents(r.URL.Query().Get("session_id")))
		case r.URL.Path == "/api/native-sessions/discover" && r.Method == http.MethodPost:
			var request *app.NativeDiscoveryRequest
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "原生发现请求无效")
				return
			}
			result, err := application.DiscoverNative(r.Context(), *request)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case r.URL.Path == "/api/native-sessions/bind" && r.Method == http.MethodPost:
			var request *app.NativeBindingRequest
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "原生来源登记请求无效")
				return
			}
			result, err := application.BindNative(r.Context(), *request)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/native/observe") && r.Method == http.MethodPost:
			var request *struct{}
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "原生来源观察请求无效")
				return
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/native/observe")
			result, err := application.ObserveBoundNative(r.Context(), id)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/native/history") && r.Method == http.MethodPost:
			var request *struct {
				Cursor string `json:"cursor,omitempty"`
			}
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "原生来源历史请求无效")
				return
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/native/history")
			result, err := application.ReadBoundNativeHistory(r.Context(), id, request.Cursor)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case r.URL.Path == "/api/native-sessions/history" && r.Method == http.MethodPost:
			var request *app.NativeHistoryRequest
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "原生历史请求无效")
				return
			}
			result, err := application.ReadNativeHistory(r.Context(), *request)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case r.URL.Path == "/api/native-sessions/preview" && r.Method == http.MethodPost:
			var request *app.NativePreviewRequest
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "原生预览请求无效")
				return
			}
			result, err := application.PreviewNative(r.Context(), *request)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/access/preview") && r.Method == http.MethodPost:
			var request *struct{}
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "只读预览请求必须为空 JSON 对象")
				return
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/access/preview")
			preview, err := application.PreviewSessionAccess(r.Context(), id)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, preview)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/skills/context") && r.Method == http.MethodPost:
			var request *struct{}
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "Skill 上下文读取请求必须为空 JSON 对象")
				return
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/skills/context")
			result, err := application.ReadSessionSkillContext(r.Context(), id)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/skills/discover") && r.Method == http.MethodPost:
			var request *struct{}
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "Skill 来源发现请求必须为空 JSON 对象")
				return
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/skills/discover")
			result, err := application.ReadSessionSkills(r.Context(), id)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/skills/catalog") && r.Method == http.MethodPost:
			var request *struct{}
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "Skill 来源清单请求必须为空 JSON 对象")
				return
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/skills/catalog")
			result, err := application.ReadSessionSkillCatalog(r.Context(), id)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/plugins/settings-stage") && r.Method == http.MethodPost:
			var request *app.PluginSettingsStageRequest
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "插件设置保存请求格式无效")
				return
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/plugins/settings-stage")
			result, err := application.StageSessionPluginSettings(r.Context(), id, *request)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/plugins/settings-preview") && r.Method == http.MethodPost:
			var request *app.PluginSettingsPreviewRequest
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "插件设置预览请求格式无效")
				return
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/plugins/settings-preview")
			result, err := application.PreviewSessionPluginSettings(r.Context(), id, *request)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/plugins/startup-settings") && r.Method == http.MethodPost:
			var request *struct{}
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "设置来源读取请求必须为空 JSON 对象")
				return
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/plugins/startup-settings")
			result, err := application.ReadSessionClaudeStartupSettings(r.Context(), id)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/plugins/installed") && r.Method == http.MethodPost:
			var request *struct{}
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "插件登记读取请求必须为空 JSON 对象")
				return
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/plugins/installed")
			result, err := application.ReadSessionInstalledPlugins(r.Context(), id)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/configuration/catalog") && r.Method == http.MethodPost:
			var request *struct{}
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "配置来源请求必须为空 JSON 对象")
				return
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/configuration/catalog")
			result, err := application.ReadSessionConfigurationCatalog(r.Context(), id)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/hooks/catalog") && r.Method == http.MethodPost:
			var request *struct{}
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "hook 来源清单请求必须为空 JSON 对象")
				return
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/hooks/catalog")
			result, err := application.ReadSessionHookCatalog(r.Context(), id)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/execution/recovery-check") && r.Method == http.MethodPost:
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/execution/recovery-check")
			var request *app.ExecutionRecoveryRequest
			if err := decode(&request); err != nil || request == nil {
				deny(w, 400, "恢复核对请求格式无效")
				return
			}
			result, err := application.ReadExecutionRecovery(r.Context(), id, *request)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/execution/preview") && r.Method == http.MethodPost:
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/execution/preview")
			var request app.ExecutionChangeRequest
			if err := decode(&request); err != nil {
				deny(w, 400, "改配预览格式无效")
				return
			}
			result, err := application.PreviewExecutionChange(id, request)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/execution") && r.Method == http.MethodGet:
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/execution")
			result, err := application.ReadExecution(id)
			if err != nil {
				deny(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case strings.HasPrefix(r.URL.Path, "/api/sessions/") && r.Method == http.MethodGet:
			limit := 50
			if raw := r.URL.Query().Get("limit"); raw != "" {
				parsed, err := strconv.Atoi(raw)
				if err != nil {
					deny(w, 400, "历史页大小无效")
					return
				}
				limit = parsed
			}
			result, err := application.ReadSession(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), r.URL.Query().Get("before"), limit)
			if err != nil {
				deny(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, result)
		case r.URL.Path == "/api/commands" && r.Method == http.MethodPost:
			var command app.Command
			if err := decode(&command); err != nil {
				deny(w, 400, "命令格式无效")
				return
			}
			receipt, err := application.Apply(command)
			if err != nil {
				deny(w, 409, err.Error())
				return
			}
			writeJSON(w, 202, receipt)
		case r.URL.Path == "/api/events" && r.Method == http.MethodGet:
			stream(w, r, application)
		default:
			deny(w, 404, "接口不存在")
		}
	})
}

func stream(w http.ResponseWriter, r *http.Request, a *app.Application) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		deny(w, 500, "不支持流式响应")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	cursor, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
	write := func(kind string, seq uint64) bool {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: {\"sequence\":%d}\n\n", seq, kind, seq)
		flusher.Flush()
		return err == nil
	}
	if r.Header.Get("Last-Event-ID") == "" {
		cursor = a.Sequence()
		if !write("snapshot", cursor) {
			return
		}
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			events, gap := a.Events(cursor)
			if gap {
				cursor = a.Sequence()
				if !write("snapshot", cursor) {
					return
				}
				continue
			}
			if len(events) > 0 {
				cursor = events[len(events)-1].Seq
				if !write("change", cursor) {
					return
				}
			}
		case <-heartbeat.C:
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func Run(ctx context.Context, c app.Config, out io.Writer) error {
	store, err := app.OpenStore(c.StateDir)
	if err != nil {
		return err
	}
	application, err := app.Open(c, store, runtime.NewAdapters(c.StateDir))
	if err != nil {
		store.Close()
		return err
	}
	defer application.Close()
	tokenPath := filepath.Join(c.StateDir, "access-token")
	b, err := os.ReadFile(tokenPath)
	if errors.Is(err, os.ErrNotExist) {
		secret := make([]byte, 32)
		if _, err = rand.Read(secret); err != nil {
			return err
		}
		b = []byte(hex.EncodeToString(secret))
		err = os.WriteFile(tokenPath, b, 0600)
	}
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(b))
	if len(token) < 32 {
		return fmt.Errorf("本地令牌格式无效")
	}
	for _, observer := range c.Observers {
		if key, ok := strings.CutPrefix(observer.TokenRef, "env:"); ok && equal(os.Getenv(key), token) {
			return fmt.Errorf("观察令牌不能复用控制台访问令牌")
		}
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	host := listener.Addr().String()
	server := &http.Server{Handler: Handler(application, host, token, web.Assets()), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	connection, _ := json.Marshal(map[string]string{"url": "http://" + host, "token_file": tokenPath})
	if err = os.WriteFile(filepath.Join(c.StateDir, "connection.json"), connection, 0600); err != nil {
		return err
	}
	fmt.Fprintf(out, "Netcap 隔离试用：http://%s\n本地访问令牌：%s\n", host, tokenPath)
	stopped := make(chan error, 1)
	go func() { stopped <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		_ = server.Close()
		return nil
	case err := <-stopped:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
