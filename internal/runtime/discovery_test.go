package runtime

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

func TestCodexDiscoveryReadsExplicitSourcesWithoutLoading(t *testing.T) {
	c, f, scope := nativeHarness(t)
	id := "018f0000-0000-7000-8000-000000000001"
	wire := map[string]any{"id": id, "name": "native title", "cwd": "/fixture", "source": "cli", "threadSource": "analytics-is-not-origin", "modelProvider": "original-provider", "status": map[string]string{"type": "notLoaded"}, "updatedAt": int64(1_700_000_000)}
	f.call = func(method string, params map[string]any) (any, error) {
		switch method {
		case "thread/list":
			if params["cwd"] != "/fixture" || params["useStateDbOnly"] != true || params["limit"] != 50 || params["archived"] != true ||
				!slices.Contains(params["sourceKinds"].([]string), "appServer") || !slices.Contains(params["sourceKinds"].([]string), "exec") {
				t.Fatalf("unsafe discovery scope: %+v", params)
			}
			return map[string]any{"data": []any{wire}, "nextCursor": "native-page"}, nil
		case "thread/read":
			if params["threadId"] != id || params["includeTurns"] != false {
				t.Fatalf("unexpected history hydration: %+v", params)
			}
			return map[string]any{"thread": wire}, nil
		default:
			return nil, fmt.Errorf("unexpected side effect: %s", method)
		}
	}
	request := DiscoveryRequest{Instance: scope.Instance, Cwd: scope.Cwd, Limit: 50, Archived: true}
	page, err := c.Discover(context.Background(), request)
	if err != nil || page.Next != "native-page" || len(page.Sessions) != 1 || page.Sessions[0].Source != "cli" || page.Sessions[0].Provider != "original-provider" {
		t.Fatalf("list: %+v %v", page, err)
	}
	record, err := c.InspectNative(context.Background(), request, id)
	if err != nil || record.NativeStatus != "notLoaded" || record.NativeID != id || record.Source != "cli" {
		t.Fatalf("preview: %+v %v", record, err)
	}
	if !slices.Equal(f.methods, []string{"thread/list", "thread/read"}) {
		t.Fatalf("discovery performed extra methods: %v", f.methods)
	}
}
