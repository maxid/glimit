// Command limits answers one question: how does one gate enforce a global
// connection cap together with a per-peer request rate?
//
// Run it with:
//
//	go run ./examples/limits
package main

import (
	"context"
	"fmt"

	"github.com/maxid/glimit"
)

const peer = "203.0.113.7"

func main() {
	ctx := context.Background()

	fmt.Println("glimit example: one gate, two dimensions")
	fmt.Println("[1/4] compile a gate: 2 global connections, 3 requests per second per peer")
	gate, err := glimit.Compile(glimit.Spec{Policies: []glimit.Policy{
		{Kind: glimit.KindConnections, Scope: glimit.ScopeGlobal, Max: 2},
		{Kind: glimit.KindRequests, Scope: glimit.ScopePeer, Rate: 3, Burst: 3},
	}})
	if err != nil {
		fmt.Println("compile failed:", err)
		return
	}

	global := glimit.Key{Kind: glimit.KindConnections, Scope: glimit.ScopeGlobal}
	for i := 1; i <= 3; i++ {
		d := gate.Allow(ctx, global, 1)
		if d.Allow {
			fmt.Printf("      connection %d admitted\n", i)
		} else {
			fmt.Printf("      connection %d rejected: %s\n", i, d.Reason)
		}
	}

	fmt.Println("[2/4] release one connection; the same slot is reusable")
	gate.Release(ctx, global, 1)
	if d := gate.Allow(ctx, global, 1); d.Allow {
		fmt.Println("      connection 4 admitted after release")
	}

	fmt.Println("[3/4] per-peer request budget: three fit, the fourth does not")
	key := glimit.Key{Kind: glimit.KindRequests, Scope: glimit.ScopePeer, ID: peer}
	for i := 1; i <= 4; i++ {
		d := gate.Allow(ctx, key, 1)
		if d.Allow {
			fmt.Printf("      request %d admitted\n", i)
			continue
		}
		fmt.Printf("      request %d rejected: %s (retry after %s)\n", i, d.Reason, d.RetryAfter.Round(1e6))
	}

	fmt.Println("[4/4] a different peer has its own budget")
	other := glimit.Key{Kind: glimit.KindRequests, Scope: glimit.ScopePeer, ID: "198.51.100.4"}
	if d := gate.Allow(ctx, other, 1); d.Allow {
		fmt.Println("      other peer admitted")
	}

	fmt.Println("[done] one contract covered both dimensions; no policy touched either")
}
