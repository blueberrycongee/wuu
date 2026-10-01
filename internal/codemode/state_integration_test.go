package codemode

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// State crosses real interpreter/process boundaries. These cases protect atomic
// commits, actor/session isolation, resource limits, and cancellation recovery.
func TestNodeStateCommitAndIsolation(t *testing.T) {
	s := nodeService(t)
	opts := RunOptions{CWD: t.TempDir(), StateScope: "actor-a"}
	run := func(service *Service, code string, options RunOptions, want string) {
		t.Helper()
		result, err := service.Run(context.Background(), RunRequest{Code: code}, options)
		if err != nil || result.Error != "" || string(result.Value) != want {
			t.Fatalf("state run=%+v err=%v, want %s", result, err, want)
		}
	}
	run(s, `const value={list:[1,2]}; store("saved",value); value.list.push(3); const first=load("saved"); first.list.push(4); globalThis.secret=1; return {saved:load("saved"),missing:typeof load("missing")};`, opts, `{"saved":{"list":[1,2]},"missing":"undefined"}`)
	run(s, `return {saved:load("saved"),global:typeof globalThis.secret};`, opts, `{"saved":{"list":[1,2]},"global":"undefined"}`)
	other := opts
	other.StateScope = "actor-b"
	run(s, `store("saved",null); return load("saved");`, other, `null`)
	run(s, `return load("saved");`, opts, `{"list":[1,2]}`)
	run(nodeService(t), `return typeof load("saved");`, opts, `"undefined"`)
	// Encoded JSON strings retain UTF-16 code units across the Go transport.
	run(s, `store("\ud800", "\udfff"); store("__proto__", {safe:true}); return true;`, opts, `true`)
	run(s, `return {code:load("\ud800").charCodeAt(0),proto:load("__proto__")};`, opts, `{"code":57343,"proto":{"safe":true}}`)
}

func TestNodeStateRejectsLossyValuesWithoutMutation(t *testing.T) {
	s := nodeService(t)
	opts := RunOptions{CWD: t.TempDir(), StateScope: "actor"}
	for _, expression := range []string{`undefined`, `NaN`, `-0`, `1n`, `({x:undefined})`, `[,,]`, `new Date()`, `({get x(){throw new Error("getter ran")}})`, `({toJSON(){return 1}})`, `(()=>{const x={};x.self=x;return x})()`} {
		result, err := s.Run(context.Background(), RunRequest{Code: `store("saved",1); let rejected=false; try {store("saved",` + expression + `)} catch(e) {rejected=e.message.includes("JSON")} return {rejected,saved:load("saved")};`}, opts)
		if err != nil || result.Error != "" || string(result.Value) != `{"rejected":true,"saved":1}` {
			t.Fatalf("lossy state %s: %+v %v", expression, result, err)
		}
	}
	result, err := s.Run(context.Background(), RunRequest{Code: `let rejected=0; for(const key of [undefined,null,1,{},[]]) {try{store(key,1)}catch{rejected++}try{load(key)}catch{rejected++}try{remove(key)}catch{rejected++}} return rejected;`}, opts)
	if err != nil || result.Error != "" || string(result.Value) != "15" {
		t.Fatalf("invalid keys=%+v %v", result, err)
	}
	for _, code := range []string{`store("saved",1)`, `load("saved")`, `remove("saved")`} {
		result, err := s.Run(context.Background(), RunRequest{Code: code}, RunOptions{CWD: opts.CWD})
		if err != nil || !strings.Contains(result.Error, "scope") {
			t.Fatalf("missing state scope=%+v %v", result, err)
		}
	}
}

func TestNodeStateFailedProgramsPreservePriorCommit(t *testing.T) {
	for _, tc := range []struct{ name, code string }{
		{"program exception", `store("saved",2); store("new",true); throw new Error("stop");`},
		{"invalid return", `store("saved",2); store("new",true); return 1n;`},
		{"failed deletion", `remove("saved"); throw new Error("stop");`},
		{"printed output overflow", `store("saved",2); store("new",true); console.log("x".repeat(4000));`},
		{"returned output overflow", `store("saved",2); store("new",true); return "x".repeat(4000);`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := nodeService(t)
			opts := RunOptions{CWD: t.TempDir(), StateScope: "actor", MaxOutputBytes: 1024}
			seed, err := s.Run(context.Background(), RunRequest{Code: `store("saved",1);`}, opts)
			if err != nil || seed.Error != "" {
				t.Fatalf("seed=%+v %v", seed, err)
			}
			failed, err := s.Run(context.Background(), RunRequest{Code: tc.code}, opts)
			if err != nil || failed.Error == "" {
				t.Fatalf("failure was accepted=%+v %v", failed, err)
			}
			result, err := s.Run(context.Background(), RunRequest{Code: `return {saved:load("saved"),missing:typeof load("new")};`}, opts)
			if err != nil || result.Error != "" || string(result.Value) != `{"saved":1,"missing":"undefined"}` {
				t.Fatalf("failed run changed committed state=%+v %v", result, err)
			}
		})
	}
}

func TestNodeStateRemovalAndQuotaReuse(t *testing.T) {
	s := nodeService(t)
	opts := RunOptions{CWD: t.TempDir(), StateScope: "actor"}
	result, err := s.Run(context.Background(), RunRequest{Code: `store("nil",null); let rejected=false; try {store("nil",undefined)} catch {rejected=true} const nil=load("nil"); const first=remove("nil"), second=remove("nil"); return {rejected,nil,first,second,missing:typeof load("nil")};`}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `{"rejected":true,"nil":null,"first":true,"second":false,"missing":"undefined"}` {
		t.Fatalf("remove/null semantics=%+v %v", result, err)
	}
	result, err = s.Run(context.Background(), RunRequest{Code: `return typeof load("nil");`}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `"undefined"` {
		t.Fatalf("deletion was not committed=%+v %v", result, err)
	}
	code := fmt.Sprintf(`for(let i=0;i<%d;i++) store("key"+i,i); remove("key0"); store("replacement",99); return {missing:typeof load("key0"),value:load("replacement")};`, MaxStateKeys)
	result, err = s.Run(context.Background(), RunRequest{Code: code}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `{"missing":"undefined","value":99}` {
		t.Fatalf("removed key capacity was not reused=%+v %v", result, err)
	}
	opts.StateScope = "bytes"
	code = fmt.Sprintf(`store("x","x".repeat(%d)); remove("x"); store("y","y".repeat(%d)); return {missing:typeof load("x"),size:load("y").length};`, MaxStateBytes-5, MaxStateBytes-5)
	result, err = s.Run(context.Background(), RunRequest{Code: code}, opts)
	if err != nil || result.Error != "" || string(result.Value) != fmt.Sprintf(`{"missing":"undefined","size":%d}`, MaxStateBytes-5) {
		t.Fatalf("removed byte capacity was not reused=%+v %v", result, err)
	}
}

func TestNodeStateCancellationAndConcurrentScopes(t *testing.T) {
	s := nodeService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	opts := RunOptions{CWD: t.TempDir(), StateScope: "actor-a", Executor: nodeExecutor(func(ctx context.Context, _ providers.ToolCall) (toolresult.Result, error) {
		close(entered)
		<-ctx.Done()
		return toolresult.Result{}, ctx.Err()
	})}
	seed, err := s.Run(context.Background(), RunRequest{Code: `store("saved",1);`}, opts)
	if err != nil || seed.Error != "" {
		t.Fatalf("seed=%+v %v", seed, err)
	}
	done := make(chan RunResult, 1)
	go func() {
		result, err := s.Run(ctx, RunRequest{Code: `store("saved",2); await tools.wait({});`, Tools: []ToolDefinition{{Name: "wait"}}}, opts)
		if err != nil {
			result.Error = err.Error()
		}
		done <- result
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("scoped run never started")
	}
	if _, err := s.Run(context.Background(), RunRequest{Code: `store("saved",3)`}, opts); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("overlapping scope accepted: %v", err)
	}
	other := opts
	other.StateScope = "actor-b"
	result, err := s.Run(context.Background(), RunRequest{Code: `store("saved",4); return load("saved");`}, other)
	if err != nil || result.Error != "" || string(result.Value) != "4" {
		t.Fatalf("independent scope blocked=%+v %v", result, err)
	}
	cancel()
	select {
	case result := <-done:
		if result.Error != context.Canceled.Error() {
			t.Fatalf("canceled state run=%+v", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation did not finish")
	}
	result, err = s.Run(context.Background(), RunRequest{Code: `return load("saved");`}, opts)
	if err != nil || result.Error != "" || string(result.Value) != "1" {
		t.Fatalf("canceled state leaked or scope remained locked=%+v %v", result, err)
	}
}

// Observe a context-aware wait without using scheduling sleeps. Admission must
// not execute another tool until the canceled program has released its executor.
type observedDoneContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *observedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestNodeStateCanceledScopeDrainsBeforeAdmission(t *testing.T) {
	for _, scenario := range []string{"next program", "new caller cancellation", "service close"} {
		t.Run(scenario, func(t *testing.T) {
			s := nodeService(t)
			entered, canceled, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
			teardown := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(released) }) }
			t.Cleanup(release)
			invoked := make(chan struct{}, 1)
			opts := RunOptions{CWD: t.TempDir(), StateScope: "actor", Executor: nodeExecutor(func(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
				if call.Name == "wait" {
					close(entered)
					<-ctx.Done()
					close(canceled)
					<-released
					close(teardown)
					return toolresult.Result{}, ctx.Err()
				}
				invoked <- struct{}{}
				select {
				case <-teardown:
					return toolresult.FromText("ready"), nil
				default:
					return toolresult.FromErrorText("prior executor is still active"), nil
				}
			})}
			seed, err := s.Run(context.Background(), RunRequest{Code: `store("saved",1);`}, opts)
			if err != nil || seed.Error != "" {
				t.Fatalf("seed=%+v %v", seed, err)
			}
			previousContext, previousCancel := context.WithCancel(context.Background())
			defer previousCancel()
			previous := make(chan RunResult, 1)
			go func() {
				result, err := s.Run(previousContext, RunRequest{Code: `store("saved",2); await tools.wait({});`, Tools: []ToolDefinition{{Name: "wait"}}}, opts)
				if err != nil {
					result.Error = err.Error()
				}
				previous <- result
			}()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("prior executor never started")
			}
			previousCancel()
			select {
			case <-canceled:
			case <-time.After(10 * time.Second):
				t.Fatal("prior executor never received cancellation")
			}
			newContext, newCancel := context.WithCancel(context.Background())
			defer newCancel()
			waiting := &observedDoneContext{Context: newContext, observed: make(chan struct{})}
			next := make(chan RunResult, 1)
			go func() {
				result, err := s.Run(waiting, RunRequest{Code: `await tools.next({}); return load("saved");`, Tools: []ToolDefinition{{Name: "next"}}}, opts)
				if err != nil {
					result.Error = err.Error()
				}
				next <- result
			}()
			select {
			case <-waiting.observed:
			case <-time.After(10 * time.Second):
				t.Fatal("next program did not reach context-aware admission")
			}
			var closed chan error
			switch scenario {
			case "new caller cancellation":
				newCancel()
			case "service close":
				closed = make(chan error, 1)
				go func() { closed <- s.Close() }()
			case "next program":
				select {
				case result := <-next:
					t.Fatalf("next program returned before drain: %+v", result)
				default:
				}
				release()
			}
			select {
			case result := <-next:
				switch scenario {
				case "next program":
					if result.Error != "" || string(result.Value) != "1" {
						t.Fatalf("next program did not resume from prior commit: %+v", result)
					}
				case "new caller cancellation":
					if result.Error != context.Canceled.Error() {
						t.Fatalf("waiting caller cancellation=%+v", result)
					}
				case "service close":
					if !strings.Contains(result.Error, "closed") {
						t.Fatalf("close did not unblock waiting caller: %+v", result)
					}
				}
			case <-time.After(10 * time.Second):
				t.Fatal("next program did not finish")
			}
			if scenario != "next program" {
				select {
				case <-invoked:
					t.Fatal("canceled waiting program invoked a tool")
				default:
				}
			}
			release()
			select {
			case result := <-previous:
				if result.Error != context.Canceled.Error() {
					t.Fatalf("prior cancellation=%+v", result)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("prior program did not finish after teardown")
			}
			if closed != nil {
				select {
				case err := <-closed:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("service close did not finish")
				}
			}
		})
	}
}

func TestNodeStateToolEffectsAreNotRolledBack(t *testing.T) {
	s := nodeService(t)
	effects := 0
	opts := RunOptions{CWD: t.TempDir(), StateScope: "actor", Executor: nodeExecutor(func(context.Context, providers.ToolCall) (toolresult.Result, error) {
		effects++
		return toolresult.FromText("completed"), nil
	})}
	result, err := s.Run(context.Background(), RunRequest{Code: `store("saved",1); await tools.effect({}); throw new Error("stop");`, Tools: []ToolDefinition{{Name: "effect"}}}, opts)
	if err != nil || result.Error != "stop" || effects != 1 {
		t.Fatalf("effect=%d result=%+v %v", effects, result, err)
	}
	result, err = s.Run(context.Background(), RunRequest{Code: `return typeof load("saved");`}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `"undefined"` || effects != 1 {
		t.Fatalf("failed program state/effect=%d result=%+v %v", effects, result, err)
	}
}

func TestNodeStateDeadlinePreservesPriorCommit(t *testing.T) {
	s := nodeService(t)
	opts := RunOptions{CWD: t.TempDir(), StateScope: "actor"}
	seed, err := s.Run(context.Background(), RunRequest{Code: `store("saved",1);`}, opts)
	if err != nil || seed.Error != "" {
		t.Fatalf("seed=%+v %v", seed, err)
	}
	result, err := s.Run(context.Background(), RunRequest{Code: `store("saved",2); for(;;){}`, TimeoutMS: 500}, opts)
	if err != nil || result.Error != context.DeadlineExceeded.Error() {
		t.Fatalf("deadline=%+v %v", result, err)
	}
	result, err = s.Run(context.Background(), RunRequest{Code: `return load("saved");`}, opts)
	if err != nil || result.Error != "" || string(result.Value) != "1" {
		t.Fatalf("timed-out program committed=%+v %v", result, err)
	}
}

func TestNodeStateCloseCancelsActiveScope(t *testing.T) {
	s := nodeService(t)
	entered := make(chan struct{})
	opts := RunOptions{CWD: t.TempDir(), StateScope: "actor", Executor: nodeExecutor(func(ctx context.Context, _ providers.ToolCall) (toolresult.Result, error) {
		close(entered)
		<-ctx.Done()
		return toolresult.Result{}, ctx.Err()
	})}
	done := make(chan RunResult, 1)
	go func() {
		result, err := s.Run(context.Background(), RunRequest{Code: `store("saved",1); await tools.wait({});`, Tools: []ToolDefinition{{Name: "wait"}}}, opts)
		if err != nil {
			result.Error = err.Error()
		}
		done <- result
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("scoped run never started")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.Error != context.Canceled.Error() {
			t.Fatalf("close=%+v", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("active state scope outlived service shutdown")
	}
}

func TestNodeStateBoundsAndReplacement(t *testing.T) {
	s := nodeService(t)
	opts := RunOptions{CWD: t.TempDir(), StateScope: "bytes"}
	code := fmt.Sprintf(`store("x","x".repeat(%d)); let rejected=false; try {store("extra",true)} catch {rejected=true} store("x",0); store("extra",true); return {rejected,x:load("x"),extra:load("extra")};`, MaxStateBytes-5)
	result, err := s.Run(context.Background(), RunRequest{Code: code}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `{"rejected":true,"x":0,"extra":true}` {
		t.Fatalf("byte bound/replacement=%+v %v", result, err)
	}
	opts.StateScope = "keys"
	code = fmt.Sprintf(`for(let i=0;i<%d;i++) store("key"+i,i); let rejected=false; try {store("extra",1)} catch {rejected=true} store("key0",99); return {rejected,value:load("key0")};`, MaxStateKeys)
	result, err = s.Run(context.Background(), RunRequest{Code: code}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `{"rejected":true,"value":99}` {
		t.Fatalf("key bound/replacement=%+v %v", result, err)
	}
	opts.StateScope = "unicode"
	code = fmt.Sprintf(`let rejected=false; try {store("x","雪".repeat(%d))} catch {rejected=true} return {rejected,missing:typeof load("x")};`, MaxStateBytes/3)
	result, err = s.Run(context.Background(), RunRequest{Code: code}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `{"rejected":true,"missing":"undefined"}` {
		t.Fatalf("UTF-8 byte bound=%+v %v", result, err)
	}
}

func TestNodeStateSessionAggregateAndScopeBounds(t *testing.T) {
	t.Run("aggregate bytes", func(t *testing.T) {
		s := nodeService(t)
		opts := RunOptions{CWD: t.TempDir()}
		code := fmt.Sprintf(`store("x","x".repeat(%d));`, MaxStateBytes-5)
		for i := 0; i < MaxServiceStateBytes/MaxStateBytes; i++ {
			opts.StateScope = fmt.Sprintf("actor-%d", i)
			opts.StateOwner = fmt.Sprintf("owner-%d", i)
			result, err := s.Run(context.Background(), RunRequest{Code: code}, opts)
			if err != nil || result.Error != "" {
				t.Fatalf("aggregate seed %d=%+v %v", i, result, err)
			}
		}
		opts.StateScope, opts.StateOwner = "overflow", "overflow-owner"
		result, err := s.Run(context.Background(), RunRequest{Code: `store("x",true);`}, opts)
		if err != nil || !strings.Contains(result.Error, "state") {
			t.Fatalf("aggregate state accepted=%+v %v", result, err)
		}
		result, err = s.Run(context.Background(), RunRequest{Code: `return typeof load("x");`}, opts)
		if err != nil || result.Error != "" || string(result.Value) != `"undefined"` {
			t.Fatalf("aggregate rejection partially committed=%+v %v", result, err)
		}
		opts.StateScope, opts.StateOwner = "actor-0", "owner-0"
		result, err = s.Run(context.Background(), RunRequest{Code: `store("x",0);`}, opts)
		if err != nil || result.Error != "" {
			t.Fatalf("cannot reduce full aggregate=%+v %v", result, err)
		}
		opts.StateScope, opts.StateOwner = "overflow", "overflow-owner"
		result, err = s.Run(context.Background(), RunRequest{Code: `store("x",true); return load("x");`}, opts)
		if err != nil || result.Error != "" || string(result.Value) != "true" {
			t.Fatalf("aggregate capacity was not released=%+v %v", result, err)
		}
		s.ForgetOwner("owner-1")
		opts.StateScope = "released-owner-capacity"
		result, err = s.Run(context.Background(), RunRequest{Code: code}, opts)
		if err != nil || result.Error != "" {
			t.Fatalf("owner deletion did not reclaim committed bytes=%+v %v", result, err)
		}
	})
	t.Run("scope count", func(t *testing.T) {
		s := nodeService(t)
		opts := RunOptions{CWD: t.TempDir()}
		for i := 0; i < MaxStateScopes; i++ {
			opts.StateScope = fmt.Sprintf("actor-%d", i)
			result, err := s.Run(context.Background(), RunRequest{Code: `store("x",1);`}, opts)
			if err != nil || result.Error != "" {
				t.Fatalf("scope seed %d=%+v %v", i, result, err)
			}
		}
		opts.StateScope = "overflow"
		if _, err := s.Run(context.Background(), RunRequest{Code: `store("x",2);`}, opts); err == nil || !strings.Contains(err.Error(), "scope") {
			t.Fatalf("scope limit accepted: %v", err)
		}
		opts.StateScope = "actor-0"
		result, err := s.Run(context.Background(), RunRequest{Code: `return load("x");`}, opts)
		if err != nil || result.Error != "" || string(result.Value) != "1" {
			t.Fatalf("scope limit evicted prior state=%+v %v", result, err)
		}
		result, err = s.Run(context.Background(), RunRequest{Code: `return remove("x");`}, opts)
		if err != nil || result.Error != "" || string(result.Value) != "true" {
			t.Fatalf("last key removal=%+v %v", result, err)
		}
		result, err = s.Run(context.Background(), RunRequest{Code: `return typeof load("x");`}, opts)
		if err != nil || result.Error != "" || string(result.Value) != `"undefined"` {
			t.Fatalf("empty scope retained deleted state=%+v %v", result, err)
		}
		opts.StateScope = "overflow"
		result, err = s.Run(context.Background(), RunRequest{Code: `store("x",2); return load("x");`}, opts)
		if err != nil || result.Error != "" || string(result.Value) != "2" {
			t.Fatalf("empty scope did not release capacity=%+v %v", result, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Run(context.Background(), RunRequest{Code: `return load("x");`}, opts); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("closed service remains usable: %v", err)
		}
	})
}

func TestNodeStateOwnerReleaseKeepsOtherConversations(t *testing.T) {
	s := nodeService(t)
	opts := RunOptions{CWD: t.TempDir()}
	for _, scope := range []string{"root", "worker", "other-root", "fork"} {
		opts.StateScope, opts.StateOwner = scope, "conversation"
		if scope == "fork" {
			opts.StateOwner = "independent-conversation"
		}
		result, err := s.Run(context.Background(), RunRequest{Code: `store("saved", 1);`}, opts)
		if err != nil || result.Error != "" {
			t.Fatalf("seed %s: %+v %v", scope, result, err)
		}
	}
	// Scope identities cannot silently migrate between lifetime owners.
	opts.StateScope, opts.StateOwner = "root", "independent-conversation"
	if _, err := s.Run(context.Background(), RunRequest{Code: `store("saved", 2);`}, opts); err == nil {
		t.Fatal("an existing scope accepted a different owner")
	}
	s.ForgetOwner("")
	s.ForgetOwner("conversation")
	for _, scope := range []string{"root", "worker", "other-root", "fork"} {
		opts.StateScope, opts.StateOwner = scope, "conversation"
		want := `"undefined"`
		if scope == "fork" {
			opts.StateOwner, want = "independent-conversation", `"number"`
		}
		result, err := s.Run(context.Background(), RunRequest{Code: `return typeof load("saved");`}, opts)
		if err != nil || result.Error != "" || string(result.Value) != want {
			t.Fatalf("owner isolation for %s: %+v %v", scope, result, err)
		}
	}
}

func TestNodeStateOwnerReleaseCancelsWaitingPrograms(t *testing.T) {
	s := nodeService(t)
	entered, canceled, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	t.Cleanup(release)
	invoked := make(chan struct{}, 1)
	opts := RunOptions{CWD: t.TempDir(), StateScope: "actor", StateOwner: "conversation", Executor: nodeExecutor(func(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
		if call.Name == "wait" {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-released
			return toolresult.Result{}, ctx.Err()
		}
		invoked <- struct{}{}
		return toolresult.FromText("unexpected"), nil
	})}
	previousContext, previousCancel := context.WithCancel(context.Background())
	defer previousCancel()
	previous := make(chan RunResult, 1)
	go func() {
		result, err := s.Run(previousContext, RunRequest{Code: `store("saved", 1); await tools.wait({});`, Tools: []ToolDefinition{{Name: "wait"}}}, opts)
		if err != nil {
			result.Error = err.Error()
		}
		previous <- result
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("prior program never entered its tool")
	}
	previousCancel()
	<-canceled
	next := make(chan RunResult, 1)
	go func() {
		result, err := s.Run(context.Background(), RunRequest{Code: `store("saved", 2); await tools.next({});`, Tools: []ToolDefinition{{Name: "next"}}}, opts)
		if err != nil {
			result.Error = err.Error()
		}
		next <- result
	}()
	// Wait for the second public Run to register before deleting its owner.
	// Its predecessor still holds the executor, so it cannot have been admitted.
	deadline := time.After(10 * time.Second)
	for {
		s.mu.Lock()
		registered := len(s.active) == 2
		s.mu.Unlock()
		if registered {
			break
		}
		select {
		case <-deadline:
			t.Fatal("waiting program was not registered for cancellation")
		default:
			runtime.Gosched()
		}
	}
	s.ForgetOwner("conversation")
	select {
	case result := <-next:
		if result.Error != context.Canceled.Error() {
			t.Fatalf("owner deletion did not cancel waiting program: %+v", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("waiting program outlived its deleted owner")
	}
	release()
	<-previous
	select {
	case <-invoked:
		t.Fatal("a waiting program ran after its owner was deleted")
	default:
	}
	result, err := s.Run(context.Background(), RunRequest{Code: `return typeof load("saved");`}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `"undefined"` {
		t.Fatalf("late completion restored deleted state: %+v %v", result, err)
	}
}

func TestNodeStateOwnerReleaseDuringSuccessfulCleanup(t *testing.T) {
	s := nodeService(t)
	cleaning, released := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	t.Cleanup(release)
	opts := RunOptions{CWD: t.TempDir(), StateScope: "actor", StateOwner: "conversation", Executor: nodeExecutor(func(ctx context.Context, _ providers.ToolCall) (toolresult.Result, error) {
		// The program has returned successfully, so normal cleanup cancels
		// execution I/O while the owning lifetime is still valid.
		<-ctx.Done()
		close(cleaning)
		<-released
		return toolresult.Result{}, ctx.Err()
	})}
	result, err := s.Run(context.Background(), RunRequest{Code: `store("saved", 1);`}, opts)
	if err != nil || result.Error != "" {
		t.Fatalf("seed=%+v %v", result, err)
	}
	done := make(chan RunResult, 1)
	go func() {
		result, err := s.Run(context.Background(), RunRequest{Code: `tools.wait({}); store("saved", 2); return "done";`, Tools: []ToolDefinition{{Name: "wait"}}}, opts)
		if err != nil {
			result.Error = err.Error()
		}
		done <- result
	}()
	select {
	case <-cleaning:
	case <-time.After(10 * time.Second):
		t.Fatal("successful program did not enter execution cleanup")
	}
	s.ForgetOwner("conversation")
	release()
	select {
	case result := <-done:
		if result.Error != context.Canceled.Error() || len(result.Value) != 0 {
			t.Fatalf("deleted owner accepted late successful completion: %+v", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("program did not finish after cleanup")
	}
	result, err = s.Run(context.Background(), RunRequest{Code: `return typeof load("saved");`}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `"undefined"` {
		t.Fatalf("successful cleanup restored deleted state: %+v %v", result, err)
	}
}

func TestNodeStateSurvivesMutableGuestIntrinsics(t *testing.T) {
	s := nodeService(t)
	opts := RunOptions{CWD: t.TempDir(), StateScope: "actor"}
	result, err := s.Run(context.Background(), RunRequest{Code: `Map.prototype.get=Map.prototype.set=Map.prototype.delete=Map.prototype.forEach=()=>{throw new Error("map hook")}; String.prototype.charCodeAt=()=>{throw new Error("string hook")}; Object.prototype.toJSON=()=>"forged"; Object.prototype.value="forged"; globalThis.JSON={stringify:()=>"forged",parse:()=>"forged"}; store("temporary",1); remove("temporary"); store("safe",{value:"雪"}); return load("safe");`}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `{"value":"雪"}` {
		t.Fatalf("intrinsics corrupt state=%+v %v", result, err)
	}
	result, err = s.Run(context.Background(), RunRequest{Code: `return load("safe");`}, opts)
	if err != nil || result.Error != "" || string(result.Value) != `{"value":"雪"}` {
		t.Fatalf("intrinsics corrupt commit=%+v %v", result, err)
	}
}
