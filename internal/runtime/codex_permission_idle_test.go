package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"codex-netcap/internal/codex"
)

func idlePermissionThread(scope Scope) codex.Thread {
	return codex.Thread{ID: scope.NativeID, Cwd: scope.Cwd, Model: scope.Route.Model, Provider: scope.Route.Provider, Status: codex.Status{Type: "idle"}}
}

func TestCodexAsyncAnswerChecksOccupancyAndPermission(t *testing.T) {
	for _, scenario := range []string{"idle", "active", "other_active_turn", "goal", "queue", "terminal", "child", "query_error", "other_question", "other_request", "unverified_request", "late_queue", "expired_during_resume", "permission_mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			replying := false
			resumes, starts, steers := 0, 0, 0
			f.call = func(method string, params map[string]any) (any, error) {
				switch method {
				case "thread/read":
					status, turnStatus := "idle", "completed"
					if replying && (scenario == "active" || scenario == "other_active_turn") {
						status, turnStatus = "active", "inProgress"
					}
					question := map[string]any{"id": "question", "type": "agentMessage", "delivery": "async", "questions": []any{map[string]any{"title": "Color?", "options": []string{"green", "blue"}}}}
					items := []any{question}
					if replying && scenario == "other_question" {
						items = append(items, map[string]any{"id": "second", "type": "agentMessage", "delivery": "async", "questions": []any{map[string]any{"title": "Another?"}}})
					}
					if scenario == "expired_during_resume" && resumes > 0 {
						items = append(items, map[string]any{"id": "external", "type": "userMessage", "text": "already answered"})
					}
					turns := []any{map[string]any{"id": "question-turn", "status": turnStatus, "items": items}}
					if replying && scenario == "other_active_turn" {
						turns = append(turns, map[string]any{"id": "another-turn", "status": "inProgress", "items": []any{}})
					}
					return map[string]any{"thread": map[string]any{"id": scope.NativeID, "cwd": scope.Cwd, "model": scope.Route.Model, "modelProvider": scope.Route.Provider, "status": map[string]string{"type": status}, "turns": turns}}, nil
				case "thread/resume":
					resumes++
					out := idlePermissionReply(scope)
					if scenario == "permission_mismatch" {
						out.Sandbox = json.RawMessage(`{"type":"readOnly"}`)
					}
					return out, nil
				case "thread/goal/get":
					if replying && scenario == "goal" {
						return map[string]any{"goal": map[string]string{"status": "active"}}, nil
					}
				case "thread/queue/list":
					if replying && scenario == "query_error" {
						return nil, &codex.RPCError{Code: -32000, Message: "queue unavailable"}
					}
					if replying && (scenario == "queue" || (scenario == "late_queue" && resumes > 0)) {
						return map[string]any{"data": []any{map[string]string{"id": "external"}}}, nil
					}
				case "thread/backgroundTerminals/list":
					if replying && scenario == "terminal" {
						return map[string]any{"data": []any{map[string]string{"processId": "working"}}}, nil
					}
				case "thread/list":
					if replying && scenario == "child" {
						return map[string]any{"data": []codex.Thread{{ID: "child", Status: codex.Status{Type: "active"}}}}, nil
					}
				case "turn/start":
					starts++
					return map[string]any{"turn": codex.Turn{ID: "answer-turn"}}, nil
				case "turn/steer":
					steers++
					if params["expectedTurnId"] != "question-turn" {
						t.Fatal("answer steered into another turn")
					}
					return map[string]string{"turnId": "question-turn"}, nil
				}
				return emptyControl(method)
			}
			view, err := c.Observe(context.Background(), scope)
			if err != nil || len(view.Pending) != 1 {
				t.Fatalf("question setup: %+v %v", view, err)
			}
			pending := view.Pending[0]
			if scenario == "other_request" {
				request := codex.Message{ID: json.RawMessage(`42`), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"threadId":"native","turnId":"question-turn"}`)}
				f.requests["42"], f.events = request, []codex.Message{request}
			}
			if scenario == "unverified_request" {
				c.pending["fixture/old/42"] = Pending{ID: "fixture/old/42", NativeID: scope.NativeID, NativeTurn: "unknown", ConnectionID: "old", Status: "unverified"}
			}
			replying = true
			_, err = c.Execute(context.Background(), Intent{ID: "reply", Kind: "respond", Scope: scope, Permission: "workspace", Pending: &pending, Answers: map[string][]string{"question-0": {"green"}}})
			switch scenario {
			case "idle":
				if err != nil || starts != 1 || steers != 0 || resumes != 1 {
					t.Fatalf("idle reply: starts=%d steers=%d resumes=%d err=%v", starts, steers, resumes, err)
				}
			case "active":
				if err != nil || starts != 0 || steers != 1 || resumes != 0 {
					t.Fatalf("active reply: starts=%d steers=%d resumes=%d err=%v", starts, steers, resumes, err)
				}
			default:
				var notSent *NotSent
				if !errors.As(err, &notSent) || starts != 0 || steers != 0 {
					t.Fatalf("unsafe reply: starts=%d steers=%d resumes=%d err=%v", starts, steers, resumes, err)
				}
				wantResumes := 0
				if scenario == "late_queue" || scenario == "expired_during_resume" || scenario == "permission_mismatch" {
					wantResumes = 1
				}
				if resumes != wantResumes {
					t.Fatalf("occupied reply sampled permission: resumes=%d want=%d", resumes, wantResumes)
				}
			}
		})
	}
}

func idlePermissionReply(scope Scope) codex.Started {
	return codex.Started{Thread: idlePermissionThread(scope), Model: scope.Route.Model, Provider: scope.Route.Provider, ApprovalPolicy: json.RawMessage(`"on-request"`), Sandbox: json.RawMessage(`{"type":"workspaceWrite"}`)}
}

func TestCodexOccupiedIdleObservationDoesNotResume(t *testing.T) {
	for _, scenario := range []string{"goal", "queue", "terminal", "child", "turn", "request", "unverified_request", "queue_error", "goal_error", "terminal_error", "child_error", "history_error"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.RecoveryPermission = "workspace"
			resumes := 0
			if scenario == "request" {
				request := codex.Message{ID: json.RawMessage(`42`), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"threadId":"native","turnId":"turn"}`)}
				f.requests["42"], f.events = request, []codex.Message{request}
			}
			if scenario == "unverified_request" {
				c.pending["fixture/old/42"] = Pending{ID: "fixture/old/42", NativeID: scope.NativeID, NativeTurn: "unknown", ConnectionID: "old", Status: "unverified"}
			}
			f.call = func(method string, params map[string]any) (any, error) {
				failure := map[string]string{"thread/queue/list": "queue_error", "thread/goal/get": "goal_error", "thread/backgroundTerminals/list": "terminal_error", "thread/list": "child_error"}
				if failure[method] == scenario || (method == "thread/read" && params["includeTurns"] == true && scenario == "history_error") {
					return nil, &codex.RPCError{Code: -32000, Message: "occupancy unavailable"}
				}
				switch method {
				case "thread/read":
					thread := idlePermissionThread(scope)
					if scenario == "turn" {
						thread.Turns = []codex.Turn{{ID: "working", Status: "inProgress"}}
					}
					return map[string]any{"thread": thread}, nil
				case "thread/resume":
					resumes++
					return idlePermissionReply(scope), nil
				case "thread/goal/get":
					if scenario == "goal" {
						return map[string]any{"goal": map[string]string{"status": "active"}}, nil
					}
				case "thread/queue/list":
					if scenario == "queue" {
						return map[string]any{"data": []any{map[string]string{"id": "queued"}}}, nil
					}
				case "thread/backgroundTerminals/list":
					if scenario == "terminal" {
						return map[string]any{"data": []any{map[string]string{"processId": "working"}}}, nil
					}
				case "thread/list":
					if scenario == "child" {
						return map[string]any{"data": []codex.Thread{{ID: "child", Status: codex.Status{Type: "active"}}}}, nil
					}
				}
				return emptyControl(method)
			}
			view, _ := c.Observe(context.Background(), scope)
			if resumes != 0 {
				t.Fatalf("permission sampling resumed occupied/unknown thread (%s): %d resumes", scenario, resumes)
			}
			if view.PermissionEvidence != nil && view.PermissionEvidence.Verified {
				t.Fatal("occupied observation produced current permission proof")
			}
		})
	}
}

func TestCodexLoadedPermissionSamplingRechecksBeforeInput(t *testing.T) {
	for _, scenario := range []string{"idle", "new_turn", "new_queue", "read_error", "connection_changed", "unloaded"} {
		t.Run(scenario, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			resumes, sends, readsAfterResume := 0, 0, 0
			conn := &permissionConnection{nativeFixture: f, identity: "initial"}
			c.clients[scope.Instance.ID] = conn
			f.call = func(method string, params map[string]any) (any, error) {
				switch method {
				case "thread/read":
					thread := idlePermissionThread(scope)
					if resumes > 0 {
						readsAfterResume++
						switch scenario {
						case "new_turn":
							thread.Status.Type = "active"
							thread.Turns = []codex.Turn{{ID: "external", Status: "inProgress"}}
						case "unloaded":
							thread.Status.Type = "notLoaded"
						case "read_error":
							return nil, &codex.RPCError{Code: -32000, Message: "post-sampling read failed"}
						}
					}
					return map[string]any{"thread": thread}, nil
				case "thread/resume":
					resumes++
					if len(params) != 2 {
						t.Fatalf("loaded policy overwritten: %+v", params)
					}
					if scenario == "connection_changed" {
						conn.identity = "replacement"
					}
					return idlePermissionReply(scope), nil
				case "thread/queue/list":
					if scenario == "new_queue" && resumes > 0 {
						return map[string]any{"data": []any{map[string]string{"id": "external"}}}, nil
					}
				case "turn/start":
					sends++
					return map[string]any{"turn": codex.Turn{ID: "sent"}}, nil
				}
				return emptyControl(method)
			}
			_, err := c.Execute(context.Background(), Intent{ID: "input", Kind: "send", Scope: scope, Permission: "workspace", Text: "input"})
			if scenario == "idle" {
				if err != nil || sends != 1 || readsAfterResume == 0 {
					t.Fatalf("idle continuation did not recheck: sends=%d reads=%d err=%v", sends, readsAfterResume, err)
				}
			} else if err == nil || sends != 0 {
				t.Fatalf("input passed changed post-sampling state: sends=%d err=%v", sends, err)
			}
			if resumes != 1 {
				t.Fatalf("sampling retried resume: %d", resumes)
			}
		})
	}
}

func TestCodexPermissionSamplingDrainsLateRequests(t *testing.T) {
	for _, phase := range []string{"before", "after"} {
		t.Run(phase, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			resumes, sends := 0, 0
			f.call = func(method string, _ map[string]any) (any, error) {
				switch method {
				case "thread/read":
					return map[string]any{"thread": idlePermissionThread(scope)}, nil
				case "thread/resume":
					resumes++
					return idlePermissionReply(scope), nil
				case "thread/list":
					if (phase == "before" && resumes == 0) || (phase == "after" && resumes > 0) {
						request := codex.Message{ID: json.RawMessage(`42`), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"threadId":"native","turnId":"working"}`)}
						f.requests["42"], f.events = request, []codex.Message{request}
					}
				case "turn/start":
					sends++
					return map[string]any{"turn": codex.Turn{ID: "unsafe"}}, nil
				}
				return emptyControl(method)
			}
			_, err := c.Execute(context.Background(), Intent{ID: "send", Kind: "send", Scope: scope, Permission: "workspace", Text: "input"})
			wantResumes := 0
			if phase == "after" {
				wantResumes = 1
			}
			if err == nil || sends != 0 || resumes != wantResumes {
				t.Fatalf("late request missed: sends=%d resumes=%d err=%v", sends, resumes, err)
			}
		})
	}
}

func TestCodexCreationPolicyInvalidatedDuringPermissionSample(t *testing.T) {
	for _, change := range []string{"history", "active", "metadata", "goal", "request"} {
		t.Run(change, func(t *testing.T) {
			c, f, scope := nativeHarness(t)
			scope.RecoveryPermission = "workspace"
			changed := false
			f.call = func(method string, _ map[string]any) (any, error) {
				switch method {
				case "thread/start":
					return idlePermissionReply(scope), nil
				case "thread/read":
					thread := idlePermissionThread(scope)
					if changed {
						switch change {
						case "history":
							thread.Turns = []codex.Turn{{ID: "external", Status: "completed"}}
						case "active":
							thread.Status.Type = "active"
						case "metadata":
							thread.Model = "changed"
						}
					}
					return map[string]any{"thread": thread}, nil
				case "thread/resume":
					changed = true
					if change == "request" {
						request := codex.Message{ID: json.RawMessage(`42`), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"threadId":"native","turnId":"external"}`)}
						f.requests["42"], f.events = request, []codex.Message{request}
					}
					return nil, &codex.RPCError{Code: -32600, Message: "no rollout found for thread id " + scope.NativeID}
				case "thread/goal/get":
					if changed && change == "goal" {
						return map[string]any{"goal": map[string]string{"status": "active"}}, nil
					}
				}
				return emptyControl(method)
			}
			if _, err := c.Execute(context.Background(), Intent{Kind: "create", Scope: scope, Permission: "workspace"}); err != nil {
				t.Fatal(err)
			}
			view, err := c.Observe(context.Background(), scope)
			if err == nil || (view.PermissionEvidence != nil && view.PermissionEvidence.Verified) || len(c.creationPolicies) != 0 {
				t.Fatalf("invalidated creation proof restored: %+v %v", view, err)
			}
		})
	}
}

func TestCodexPausedGoalAllowsLoadedPermissionSampling(t *testing.T) {
	c, f, scope := nativeHarness(t)
	scope.RecoveryPermission = "workspace"
	resumes, goals := 0, 0
	f.call = func(method string, params map[string]any) (any, error) {
		switch method {
		case "thread/read":
			return map[string]any{"thread": idlePermissionThread(scope)}, nil
		case "thread/resume":
			resumes++
			if goals != 1 || len(params) != 2 {
				t.Fatal("loaded resume did not wait for the paused Goal check or changed policy")
			}
			return idlePermissionReply(scope), nil
		case "thread/goal/get":
			goals++
			return map[string]any{"goal": map[string]string{"status": "paused", "objective": "synthetic"}}, nil
		}
		return emptyControl(method)
	}
	view, err := c.Observe(context.Background(), scope)
	if err != nil || !view.ExecutionClear || view.PermissionEvidence == nil || !view.PermissionEvidence.Verified || resumes != 1 || goals != 2 {
		t.Fatalf("paused Goal observation: resumes=%d goals=%d view=%+v err=%v", resumes, goals, view, err)
	}
}
