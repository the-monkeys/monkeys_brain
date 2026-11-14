package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func boundedCatalogCall(es *elasticsearchStorage, method string, ctx context.Context, limit, offset int) ([]map[string]interface{}, int, error) {
	if method == "helper" {
		return es.publicCatalogScroll(ctx, map[string]interface{}{"track_total_hits": true}, limit, offset)
	}
	return catalogRangeCall(es, method, ctx, limit, offset)
}

// Catch oversized requests through either DB entry point as well as the scroll
// helper. The transport serves only the requested batch, just as ES would.
func TestPublicCatalogPageBounds(t *testing.T) {
	for _, method := range []string{"published", "tags", "helper"} {
		for _, offset := range []int{0, 10000} {
			for _, tc := range []struct{ limit, want int }{
				{2, 2}, {500, 500}, {1000, 1000}, {1001, 1000}, {2147483647, 1000},
			} {
				t.Run(fmt.Sprintf("%s/offset=%d/limit=%d", method, offset, tc.limit), func(t *testing.T) {
					searches, scrolls, clears, next := 0, 0, 0, 0
					scrolling := method == "helper" || offset == 10000
					es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
						var body map[string]interface{}
						if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
							t.Fatal(err)
						}
						if req.Method == http.MethodDelete {
							clears++
							return scopeResponse(200, `{"succeeded":true,"num_freed":1}`), nil
						}
						start, size := next, 1000
						if strings.HasSuffix(req.URL.Path, "/_search") {
							searches++
							start = int(body["from"].(float64))
							size = int(body["size"].(float64))
							if size > 1000 {
								t.Fatalf("unbounded search size: %d", size)
							}
							if !scrolling && (req.URL.Query().Get("scroll") != "" || start != offset || size != tc.want) {
								t.Fatalf("ordinary from/size path changed: %v", body)
							}
						} else {
							scrolls++
						}
						next = start + size
						id := ""
						if scrolling {
							id = fmt.Sprintf("bounded-scroll-%d", next)
						}
						return catalogJSONResponse(t, 200, catalogGeneratedPage(start, size, 20000, id)), nil
					})
					rows, total, err := boundedCatalogCall(es, method, context.Background(), tc.limit, offset)
					if err != nil || total != 20000 || len(rows) != tc.want {
						t.Fatalf("count=%d total=%d err=%v; want count=%d total=20000", len(rows), total, err, tc.want)
					}
					for i, row := range rows {
						if row["blog_id"] != fmt.Sprintf("post-%d", offset+i) {
							t.Fatalf("wrong page at %d: %v", i, row["blog_id"])
						}
					}
					wantScrolls, wantClears := 0, 0
					if scrolling {
						wantScrolls, wantClears = offset/1000, 1
					}
					if searches != 1 || scrolls != wantScrolls || clears != wantClears {
						t.Fatalf("searches=%d scrolls=%d clears=%d; want 1/%d/%d", searches, scrolls, clears, wantScrolls, wantClears)
					}
				})
			}
		}
	}
}

// Virtual time lets blocked transports exercise the real deadline and cleanup
// timers without sleeping in real time or opening sockets.
func TestPublicCatalogTraversalBudget(t *testing.T) {
	for _, method := range []string{"published", "tags", "helper"} {
		for _, phase := range []string{"initial", "continuation", "cleanup", "earlier caller deadline", "caller cancellation"} {
			t.Run(method+"/"+phase, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					start := time.Now()
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					wantElapsed := 10 * time.Second
					if phase == "earlier caller deadline" {
						var stop context.CancelFunc
						ctx, stop = context.WithTimeout(ctx, 2*time.Second)
						defer stop()
						wantElapsed = 2 * time.Second
					}
					if phase == "cleanup" {
						wantElapsed = 13 * time.Second
					}
					if phase == "caller cancellation" {
						wantElapsed = 4 * time.Second
					}
					searches, scrolls, clears := 0, 0, 0
					var firstDeadline time.Time
					es := scopeStorage(t, func(req *http.Request) (*http.Response, error) {
						if req.Method == http.MethodDelete {
							clears++
							if req.Context().Err() != nil {
								t.Fatal("cleanup inherited cancellation")
							}
							deadline, ok := req.Context().Deadline()
							if !ok || deadline.Sub(time.Now()) > 3*time.Second {
								t.Fatal("cleanup has no bounded independent deadline")
							}
							if phase == "cleanup" {
								<-req.Context().Done()
								return nil, req.Context().Err()
							}
							return scopeResponse(200, `{"succeeded":true,"num_freed":1}`), nil
						}
						deadline, ok := req.Context().Deadline()
						if !ok || deadline.Sub(start) > 10*time.Second {
							t.Fatal("catalog traversal has no overall deadline of at most 10 seconds")
						}
						initial := strings.HasSuffix(req.URL.Path, "/_search")
						if initial {
							searches++
							firstDeadline = deadline
							if phase != "initial" && phase != "earlier caller deadline" {
								time.Sleep(4 * time.Second)
								return catalogJSONResponse(t, 200, catalogGeneratedPage(0, 1000, 20000, "budget-scroll")), nil
							}
						} else {
							scrolls++
							if !deadline.Equal(firstDeadline) {
								t.Fatal("continuation restarted the total traversal budget")
							}
							if phase == "caller cancellation" {
								cancel()
							}
						}
						<-req.Context().Done()
						return nil, req.Context().Err()
					})
					rows, _, err := boundedCatalogCall(es, method, ctx, 2, 10000)
					wantErr := context.DeadlineExceeded
					if phase == "caller cancellation" {
						wantErr = context.Canceled
					}
					if rows != nil || !errors.Is(err, wantErr) {
						t.Fatalf("rows=%v err=%v; want no partial result and %v", rows, err, wantErr)
					}
					if elapsed := time.Since(start); elapsed != wantElapsed {
						t.Fatalf("total work took %v, want %v", elapsed, wantElapsed)
					}
					wantScrolls, wantClears := 1, 1
					if phase == "initial" || phase == "earlier caller deadline" {
						wantScrolls, wantClears = 0, 0
					}
					if searches != 1 || scrolls != wantScrolls || clears != wantClears {
						t.Fatalf("searches=%d scrolls=%d clears=%d; want 1/%d/%d", searches, scrolls, clears, wantScrolls, wantClears)
					}
				})
			})
		}
	}
}
