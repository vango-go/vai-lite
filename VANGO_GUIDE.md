# Vango Developer Guide

## Preface

### Version and status

* **Document:** Vango Developer Guide (v2)
* **Status:** Beta prep draft
* **Audience:** developers and coding agents building Vango apps
* **Scope:** how to build Vango apps correctly and idiomatically

This guide is intentionally **application-focused**.

It tells you:

* which APIs to use
* which patterns are blessed
* which constraints are required for correctness
* how to structure real Vango apps
* how to avoid the common footguns

It does **not** try to teach Vango internals in detail.

You do not need to understand:

* VDOM patch encoding
* WebSocket frame layouts
* manifest internals
* runtime binding internals
* session serialization internals

You do need to internalize:

* the session loop is the single writer
* Setup allocates state
* render stays pure
* Resources and Actions own blocking work
* list identity is explicit
* persisted state is a deliberate design choice

When this guide says **MUST**, treat it as a real contract.

When this guide says **SHOULD**, treat it as the default path unless you have a specific reason not to.

---

### What Vango optimizes for

Vango is built for:

* full-stack Go apps
* server-owned UI state
* SPA-feeling navigation and updates
* strong correctness constraints
* AI-friendly code generation and refactoring
* deploy-safe persisted state

Vango is not trying to be an unstructured bag of escape hatches.

Vango has a strong preferred path:

* `vango.Setup(...)` for stateful components
* `setup.*` helpers for reactive allocation
* `el` for view construction
* server-driven rendering by default
* explicit client boundaries when needed

---

### The core promise

If you follow the model:

* state lives on the server
* rendering is deterministic
* async work is structured
* deploy impact is visible
* refactors are safer

If you fight the model:

* patch correctness degrades
* persistence gets risky
* session behavior gets harder to reason about
* generated tooling becomes less helpful

---

### Reading conventions

This guide uses RFC-style keywords:

* **MUST** / **MUST NOT**
* **SHOULD** / **SHOULD NOT**
* **MAY**

Unless otherwise noted:

* examples use Go 1.26 style generics
* examples use `vango`, `setup`, and `el`
* stateful components use `vango.Setup`
* children are represented with `vango.Slot`
* lists use `RangeKeyed`

---

### Imports policy

For most Vango app code, start here:

```go
import "github.com/vango-go/vango"
import "github.com/vango-go/vango/setup"
import . "github.com/vango-go/vango/el"
```

Add feature packages only when the feature requires them:

* `github.com/vango-go/vango/pkg/auth`
* `github.com/vango-go/vango/pkg/authmw`
* `github.com/vango-go/vango/pkg/auth/sessionauth`
* `github.com/vango-go/vango/pkg/toast`
* `github.com/vango-go/vango/pkg/upload`
* `github.com/vango-go/vango/pkg/urlparam`
* `github.com/vango-go/vango/pkg/vtest`

Prefer **not** to build new app code directly on:

* `github.com/vango-go/vango/pkg/vango`
* `github.com/vango-go/vango/pkg/vdom`
* `github.com/vango-go/vango/pkg/server`
* most `github.com/vango-go/vango/pkg/features/*` packages

Those packages are lower-level or compatibility-oriented surfaces.

The blessed app-authoring surface is:

* `vango`
* `setup`
* `el`

---

## Part I — The Vango Model

### 1. Server-driven UI

In Vango:

* the server owns state
* the server renders UI
* the browser sends events
* the browser applies patches

The browser is not the source of truth.

That changes how you should think about app code:

* UI is a projection of server state
* event handlers mutate server state
* render is recomputed on the server
* the client is thin by default

This is why Vango apps can feel SPA-like without a large client state layer.

---

### 2. Sessions and the session loop

Each connected browser tab has a session.

Each session has a single-writer event loop.

That means:

* one event runs at a time
* signal writes are serialized
* rerenders happen deterministically
* most accidental write races disappear

The most important rule in the framework is:

> All reactive writes MUST happen on the session loop.

If code runs off the loop, it must come back through a structured framework boundary.

---

### 3. Where code runs

You should know which code is allowed to do what.

| Location | Context | Blocking I/O | Signal allocation | Signal writes |
| --- | --- | --- | --- | --- |
| Setup callback | SSR or session loop | No | Yes | No |
| Render closure | SSR or session loop | No | No | No |
| Event handler | session loop | No | No | Yes |
| Resource loader | worker | Yes | No | No |
| Action work | worker | Yes | No | No |
| Hook code | browser | N/A | N/A | browser-local only |
| Island/WASM code | browser | N/A | N/A | browser-local only |

If you need to mutate state from code running off-loop:

* prefer Resource or Action
* or use `ctx.Dispatch(...)` when integrating with external callbacks

Do not invent your own concurrency model.

---

### 4. Initial load, mount, reconnect

Typical initial request flow:

1. browser requests a page
2. server matches the route
3. server renders HTML
4. browser shows content
5. client script connects to the live runtime

Interactive event flow:

1. user triggers an event
2. thin client sends event data
3. server resolves the handler
4. handler runs on the session loop
5. render recomputes
6. patches are sent
7. browser applies the patches

Reconnect flow:

1. browser reconnects within the resume window
2. server tries to resume the session
3. if resume is valid, interactivity continues
4. if resume is invalid, the client self-heals with a fresh load

Common resume failure reasons:

* session evicted
* auth revalidation failed
* schema incompatibility
* persisted blob corruption
* patch mismatch

The live runtime mounts the current page through:

* `/_vango/live?path=<current-path-and-query>`

That contract matters in real apps.

If you proxy Vango, mount it under a subpath, or customize runtime bootstrapping:

* preserve the full current path and query string
* preserve the `?path=` parameter exactly
* ensure SSR and WS mount resolve the same route

If SSR and WS mount disagree, you can get:

* handler lookup mismatches
* patch mismatches
* reconnect loops
* state that appears attached to the wrong page

Vango also enforces a maximum decoded `?path=` size.

Treat unusually large query strings as suspect rather than relying on them for normal app state.

---

### 5. Purity and scheduling

Vango correctness depends on strong separation of responsibilities.

Setup:

* allocates state
* registers lifecycle
* stays non-blocking

Render:

* computes UI from current state
* stays deterministic
* does not allocate
* does not write state

Resources and Actions:

* own blocking work
* run off-loop
* return results through the framework

Effects:

* run after commit
* react to dependency changes
* stay bounded and non-blocking

If a pattern breaks those boundaries, it is almost always the wrong pattern.

---

### 6. The performance mental model

The session loop is the expensive place.

Keep these cheap:

* render closures
* event handlers
* synchronous effect bodies

Move expensive work to:

* Resource loaders
* Action work functions

Also remember:

* unstable list keys create large patches
* uncontrolled client DOM mutation causes patch mismatches
* too much persisted state hurts resume time

The best default strategy is:

* compute, do not fetch, during render
* key dynamic lists
* persist references, not blobs

---

## Part II — The Blessed Component Model

### 7. Stateful components always use `vango.Setup`

The canonical shape is:

```go
type CounterProps struct {
	Initial  int
	Children vango.Slot
}

func Counter(p CounterProps) vango.Component {
	return vango.Setup(p, func(s vango.SetupCtx[CounterProps]) vango.RenderFn {
		props := s.Props()
		count := setup.Signal(&s, props.Peek().Initial)

		return func() *vango.VNode {
			p := props.Get()
			return Div(
				Button(OnClick(count.Dec), Text("-")),
				Span(Textf("%d", count.Get())),
				Button(OnClick(count.Inc), Text("+")),
				p.Children,
			)
		}
	})
}
```

Rules:

* stateful component functions SHOULD be `func(p PropsType) vango.Component`
* `PropsType` SHOULD be a named struct
* if there are no props, use `vango.NoProps`
* Setup gets the incoming props value directly
* Setup returns exactly one render closure

Do not create new stateful authoring patterns in app code.

---

### 8. Props

Inside Setup:

```go
props := s.Props()
```

You get a reactive props cell:

* `props.Get()` -> tracked read
* `props.Peek()` -> untracked read

Use `Get()` in render:

```go
return func() *vango.VNode {
	p := props.Get()
	return Div(Text(p.Title))
}
```

Use `Peek()` for initialization:

```go
name := setup.Signal(&s, props.Peek().InitialName)
```

Props are runtime-only.

Props do not persist.

Props updates do not rerun Setup.

---

### 9. Slots

Children in Setup components are explicit.

Use `vango.Slot` in props:

```go
type CardProps struct {
	Title    string
	Children vango.Slot
}
```

Render slots directly:

```go
return Div(
	H2(Text(p.Title)),
	p.Children,
)
```

You may define multiple named slots:

```go
type PageProps struct {
	Header   vango.Slot
	Sidebar  vango.Slot
	Children vango.Slot
	Footer   vango.Slot
}
```

Construct slots with `vango.Children(...)` when needed.

`vango.Children(...)` has a few important normalization rules:

* `nil` and `false` are skipped
* `true` is not renderable and should be treated as a bug
* `""` and `0` still render
* supported nested containers are flattened deterministically

Do not rely on ad hoc child filtering behavior in your own helpers.

Do not invent ad hoc `[]any` children props for stateful components.

---

### 10. Stateless helpers are encouraged

Not every view helper should be a component.

This is good:

```go
func Badge(label string) *vango.VNode {
	return Span(
		Class("inline-flex rounded px-2 py-1 text-xs"),
		Text(label),
	)
}
```

Use a plain `func(...) *vango.VNode` when:

* no reactive allocation is needed
* the helper is just view composition
* the helper is deterministic

Use `vango.Setup` only when you actually need component state or lifecycle.

---

### 11. Setup allocation rules

These rules replace hook-order style footguns.

Within a Setup callback, reactive allocation MUST be unconditional.

Bad:

```go
if props.Peek().Admin {
	_ = setup.Signal(&s, true)
}
```

Bad:

```go
for _, x := range items {
	_ = setup.Signal(&s, x)
}
```

Bad:

```go
return func() *vango.VNode {
	_ = setup.Signal(&s, 0)
	return Div()
}
```

Good:

```go
adminFlag := setup.Signal(&s, false)

return func() *vango.VNode {
	if !props.Get().Admin {
		return Div()
	}
	return Div(Textf("%t", adminFlag.Get()))
}
```

Rules:

* no conditional allocation
* no variable-count allocation
* no allocation in render
* no blocking I/O in Setup
* no goroutines in Setup

Also prefer one `vango.Setup(...)` call per function.

Multiple Setup callsites in one function make refactors and persistence continuity harder to reason about.

---

### 12. Setup lifecycle methods

`SetupCtx` gives you:

* `s.Ctx()`
* `s.OnMount(...)`
* `s.Effect(...)`
* `s.OnChange(...)`
* `s.OnPersistError(...)`

Use `s.Ctx()` when you need the runtime context during Setup:

```go
ctx := s.Ctx()
```

That is commonly used for:

* SessionKey reads and writes
* logger or tracing propagation
* route-aware initialization

---

### 13. `OnMount`

`OnMount` is for one-time post-commit setup.

```go
s.OnMount(func() vango.Cleanup {
	// start non-blocking integration
	return func() {
		// cleanup
	}
})
```

Good uses:

* subscription setup
* post-commit integration
* analytics

Bad uses:

* blocking I/O
* long-running manual goroutines
* allocating reactive primitives

---

### 14. `Effect`

Effects run after commit and rerun when dependencies change.

```go
query := setup.Signal(&s, "")
debounced := setup.Signal(&s, "")

s.Effect(func() vango.Cleanup {
	q := query.Get()
	if q == "" {
		debounced.Set("")
		return nil
	}
	return vango.Timeout(300*time.Millisecond, func() {
		debounced.Set(q)
	})
})
```

Rules:

* keep effects bounded
* do not block
* do not spawn raw goroutines
* use framework helpers like `Timeout`, `Interval`, `Subscribe`, `GoLatest`

If an effect synchronously writes state, be sure the pattern is intentional and stable.

---

### 15. `OnChange`

Use `setup.OnChange` or `s.OnChange` for explicit orchestration when one value changing should trigger some other synchronous state transition.

Typed form:

```go
setup.OnChange(&s,
	func() int { return props.Get().UserID },
	func(next, prev int) {
		if next != prev {
			selected.Set("")
		}
	},
)
```

This is preferable to hiding orchestration in render.

Use it for:

* resetting dependent state
* coordinating action success
* clearing local errors on key changes

---

## Part III — Reactive State and Async Work

### 16. Local signals

Allocate local state with:

```go
count := setup.Signal(&s, 0)
```

Operations:

* `Get()`
* `Peek()`
* `Set(v)`
* `Update(fn)`

There are also typed convenience methods on signals for common container and numeric operations.

Examples:

```go
count.Inc()
count.Dec()
count.Add(5)
```

Container-style helpers exist too:

```go
items.Append(item)
items.RemoveAt(i)
items.UpdateAt(i, func(cur Item) Item {
	cur.Done = true
	return cur
})
items.RemoveWhere(func(it Item) bool {
	return it.Done
})
```

Prefer copy-on-write semantics and helper methods over in-place mutation.

This is not just style.

If you mutate a slice or map in place and then write back the same backing container, you can get stale UI or surprising no-op updates.

Prefer:

```go
items.Update(func(cur []Item) []Item {
	next := append([]Item(nil), cur...)
	next[i] = updated
	return next
})
```

If the framework or tooling reports an in-place mutation warning or panic, treat it as a real correctness issue.

---

### 17. Shared signals

Allocate session-scoped shared persisted state with:

```go
cartIDs := setup.SharedSignal(&s, []string{})
```

A SharedSignal is:

* session-scoped
* shared across the session
* persisted when a store is configured

Important:

* it is not per-component-instance state
* multiple component instances using the same SharedSignal allocation site share the same underlying cell

Use SharedSignal for:

* cart IDs
* small per-session filters
* session-scoped preferences
* cross-page progress state

---

### 18. Global signals

Allocate global persisted state with:

```go
announcement := setup.GlobalSignal(&s, "")
```

A GlobalSignal is:

* app-scoped
* shared across sessions
* persisted in the global store
* optionally broadcast in real time when a broadcast backend exists

Use GlobalSignal sparingly.

It is appropriate for:

* announcements
* feature gates
* system-wide indicators

It is not the default choice for normal per-user app state.

---

### 19. Session keys

Typed session keys are package-scoped persisted KV.

Declaration:

```go
type ThemePrefs struct {
	Mode string
}

func (ThemePrefs) VangoSchemaID() string {
	return "myapp:ThemePrefs:v1"
}

var ThemeKey = vango.NewSessionKey[ThemePrefs](
	"theme",
	vango.Default(ThemePrefs{Mode: "system"}),
)
```

Usage:

```go
ctx := s.Ctx()
prefs := ThemeKey.Get(ctx)
_ = ThemeKey.Set(ctx, ThemePrefs{Mode: "dark"})
ThemeKey.Delete(ctx)
```

Use SessionKey when:

* the state is session-scoped
* the state does not naturally belong to one component allocation site
* typed persisted KV is clearer than a shared signal

---

### 20. Memos

Memos represent derived state.

```go
total := setup.Memo(&s, func() int {
	sum := 0
	for _, item := range cart.Get() {
		sum += item.Qty
	}
	return sum
})
```

Rules:

* memos are derived only
* memos never persist
* memo dependencies come from the reads inside the compute function

Avoid hidden dependencies.

Prefer:

```go
display := setup.Memo(&s, func() string {
	show := showDetails.Get()
	full := details.Get()
	short := summary.Get()
	if show {
		return full
	}
	return short
})
```

Instead of conditionally reading different signals in different branches when it harms clarity.

---

### 21. Resources

Resources are for async reads.

Simple resource:

```go
profile := setup.Resource(&s,
	func(ctx context.Context) (*Profile, error) {
		return loadProfile(ctx)
	},
)
```

Keyed resource:

```go
user := setup.ResourceKeyed(&s,
	func() int { return props.Get().UserID },
	func(ctx context.Context, id int) (*User, error) {
		return loadUser(ctx, id)
	},
)
```

Resource render pattern:

```go
return user.Match(
	vango.OnLoading(func() *vango.VNode {
		return Div(Text("Loading..."))
	}),
	vango.OnError(func(err error) *vango.VNode {
		return Div(Text(err.Error()))
	}),
	vango.OnReady(func(u *User) *vango.VNode {
		return Div(Text(u.Name))
	}),
)
```

Rules:

* loaders may block
* loaders must honor `context.Context`
* loaders must not write reactive state directly
* key functions run on-loop and should be deterministic

Use a keyed resource whenever props, URL state, or local signals determine what is being loaded.

Operationally important semantics:

* key changes cancel stale in-flight work
* unmount cancels in-flight work
* loader errors become Resource error state, not panics in app code
* loaders must never write signals directly

Render Resource state explicitly.

Do not hide loading or error behavior behind side effects.

---

### 22. Actions

Actions are for async writes and mutations.

```go
save := setup.Action(&s,
	func(ctx context.Context, in SaveInput) (*SavedProfile, error) {
		return saveProfile(ctx, in)
	},
	vango.DropWhileRunning(),
)
```

Use actions from event handlers or other session-loop code:

```go
Button(
	OnClick(func() {
		save.Run(SaveInput{Name: name.Get()})
	}),
	Text("Save"),
)
```

Observe state:

* `ActionIdle`
* `ActionRunning`
* `ActionSuccess`
* `ActionError`

Render from action state:

```go
save.Match(
	vango.OnActionRunning(func() *vango.VNode {
		return Div(Text("Saving..."))
	}),
	vango.OnActionError(func(err error) *vango.VNode {
		return Div(Text(err.Error()))
	}),
	vango.OnActionSuccess(func(_ *SavedProfile) *vango.VNode {
		return Div(Text("Saved"))
	}),
)
```

Concurrency options:

* `vango.CancelLatest()`
* `vango.DropWhileRunning()`
* `vango.Queue(n)`

Default mental model:

* search-like work -> `CancelLatest`
* submit/delete/save -> `DropWhileRunning`
* ordered background work -> `Queue`

Operationally important semantics:

* work is canceled automatically on unmount
* canceled work is not the same thing as an application error
* canceled or deadline-hit runs should usually be treated as returning to idle rather than "business failure"
* panic in work is recovered by the framework and surfaced as Action error state
* `Run(...)` must only happen on the session loop

---

### 23. Action rejections

Rejected action runs are not work failures.

Examples:

* queue full
* dropped while already running

If you care about that distinction, register `vango.OnActionRejected(...)`.

`Run(...)` returns `false` when policy rejects the run.

That does not mean the current Action became `ActionError`.

That is appropriate for:

* metrics
* debug logs
* toast messages

Do not treat every rejected run as an application error.

---

### 24. Transactions

Use `vango.Tx(...)` or `vango.TxNamed(...)` when one user action updates multiple pieces of state that should commit together.

```go
vango.TxNamed("Cart:AddItem", func() {
	cartIDs.Append(id)
	toastText.Set("Added to cart")
})
```

Why:

* clearer intent
* fewer intermediate rerenders
* better observability
* atomicity for persisted writes

If a transaction includes persisted writes and persistence fails:

* the transaction aborts
* partial durable commits are not allowed

---

### 25. URL params

Use `setup.URLParam` for shareable state that belongs in the URL.

```go
import "github.com/vango-go/vango/pkg/urlparam"

query := setup.URLParam(&s, "q", "", urlparam.Replace, urlparam.Debounce(300*time.Millisecond))
page := setup.URLParam(&s, "page", 1, urlparam.Replace)
```

For flat struct encoding:

```go
type Filters struct {
	Status string `url:"status"`
	Owner  string `url:"owner"`
}

filters := setup.URLParam(&s, "", Filters{}, urlparam.WithEncoding(urlparam.EncodingFlat))
```

Encodings:

* `EncodingFlat`
* `EncodingJSON`
* `EncodingComma`

Modes:

* `urlparam.Push`
* `urlparam.Replace`

Guidelines:

* use `Replace` for search/filter inputs
* use `Push` when each change is a meaningful navigation step
* use debounce for keystroke-driven params
* key Resources off URL params when the URL drives the view

---

### 26. Concurrency rules

Do not start manual goroutines in:

* Setup
* render
* event handlers
* `OnMount`
* `Effect`
* `OnChange`

Do not do blocking work on the session loop.

Use:

* Resource
* Action
* `Timeout`
* `Interval`
* `Subscribe`
* `GoLatest`
* `ctx.Dispatch(...)` only for external callback integration

This rule is not optional.

It is the core of Vango correctness.

---

## Part IV — Building Views With `el`

### 27. The `el` package

`el` is the UI DSL.

It provides:

* HTML constructors
* SVG constructors
* attribute helpers
* event helpers
* control-flow helpers
* list helpers
* client boundary helpers

Examples:

```go
Div(
	Class("p-4"),
	H1(Text("Hello")),
	Button(OnClick(doThing), Text("Click")),
)
```

---

### 28. Elements and attributes

Use the obvious constructor and attribute names:

* `Div`, `Span`, `Button`, `Input`, `Form`, `Main`, `Nav`, `Section`
* `Class`, `ID`, `Href`, `Type`, `Value`, `Checked`, `Placeholder`, `Disabled`

Compose many attributes directly:

```go
Input(
	Type("email"),
	Name("email"),
	Value(email.Get()),
	Placeholder("you@example.com"),
	OnInput(email.Set),
)
```

Use `Classes(...)`, `ClassIf(...)`, and `AttrIf(...)` when that keeps the code clearer.

---

### 29. Events

The `el` package exposes typed DOM event helpers:

* `OnClick`
* `OnInput`
* `OnChange`
* `OnSubmit`
* `OnKeyDown`
* pointer, mouse, drag, scroll, touch, and transition helpers

Prefer the most specific helper available.

Examples:

```go
Button(OnClick(save), Text("Save"))

Input(OnInput(name.Set))

Form(OnSubmit(vango.PreventDefault(func() {
	submit.Run(form.Values())
})))
```

For client hooks:

* `Hook("name", config)`
* `OnEvent("name", handler)`

For islands and WASM:

* `OnIslandMessage(...)`
* `OnWASMMessage(...)`

---

### 30. Conditional rendering helpers

Vango ships several helpers for clear conditional view composition:

* `If`
* `IfElse`
* `When`
* `Unless`
* `Either`
* `Switch`
* `Case_`
* `Maybe`
* `Show`
* `ShowWhen`

Use the helper that makes the branch structure obvious.

Examples:

```go
If(errText.Get() != "", Div(Text(errText.Get())))
```

```go
IfElse(loading.Get(),
	Div(Text("Loading...")),
	Div(Text("Ready")),
)
```

Do not overuse clever nested helpers when a normal Go `if` in the render closure is clearer.

---

### 31. Lists and identity

For dynamic lists, use `RangeKeyed`.

```go
RangeKeyed(items,
	func(it Item) string { return it.ID },
	func(it Item) *vango.VNode {
		return Li(Text(it.Name))
	},
)
```

Never use:

* indices
* random keys
* mutable display strings as identity

For maps, use `RangeMap` when you truly want to iterate a map directly.

`RangeMap` exists because raw Go map iteration order is intentionally unstable.

Treat `RangeMap` as a deterministic helper, not a promise that maps are a good default UI data structure.

If ordering matters and you care about full control:

* transform the map into a slice
* sort it explicitly
* render it with `RangeKeyed`

If a list contains hooks, islands, or WASM boundaries, stable list identity becomes even more important because DOM boundary identity must survive reorder and filtering correctly.

---

### 32. Raw HTML

Default text rendering is escaped.

This is good:

```go
Text(userInput)
Textf("Hello, %s", userName)
```

Raw HTML must be explicit:

```go
DangerouslySetInnerHTML(SanitizeTrustedHTML(trustedHTML))
```

Treat raw HTML as dangerous.

Do not feed user HTML into a raw sink without a real sanitizer.

Prefer typed Vango view code whenever possible.

---

### 33. Scripts and client bootstrap

Most layouts should include:

```go
VangoScripts()
```

You may add script options when needed:

* `WithScriptPath(...)`
* `WithCSRFToken(...)`
* `WithDebug()`
* `WithoutDefer()`

For islands and WASM modules, same-origin loading should remain the default.

If you intentionally allow trusted cross-origin module loading, keep the allowlist explicit and local to the layout:

* `WithModuleOrigins(...)`
* `WithAllowInsecureModuleOrigins()` only as a break-glass local/dev setting

For module-origin configuration and special security cases, keep the options close to the layout so the trust boundary is obvious.

---

### 34. Test IDs

Use `data-testid` for test queries.

Helper:

```go
func TestID(id string) vango.Attr {
	return Attr("data-testid", id)
}
```

Use test IDs on:

* buttons you click in tests
* inputs you type into
* important output text nodes
* structural containers you snapshot or query often

---

## Part V — Project Structure, Routing, and APIs

### 35. Canonical project structure

Recommended app layout:

```text
myapp/
├── cmd/
│   └── server/
│       └── main.go
├── app/
│   ├── routes/
│   │   ├── routes_gen.go
│   │   ├── layout.go
│   │   ├── index.go
│   │   ├── about.go
│   │   ├── projects/
│   │   │   ├── layout.go
│   │   │   ├── index.go
│   │   │   └── id_/
│   │   │       └── index.go
│   │   └── api/
│   │       └── health.go
│   ├── components/
│   ├── stores/
│   └── middleware/
├── internal/
│   ├── services/
│   ├── db/
│   └── config/
├── public/
├── vango.json
├── vango_state_manifest.json
├── vango_state_schema.json
└── go.mod
```

Intent:

* `app/routes` owns route files
* `app/components` owns shared UI
* `app/stores` owns SessionKeys and durable store-like surfaces
* `internal/services` owns business logic and I/O
* `public` owns static assets

---

### 36. The entry point

Typical `cmd/server/main.go`:

```go
func main() {
	app, err := vango.New(vango.Config{
		Session: vango.SessionConfig{
			ResumeWindow: vango.ResumeWindow(30 * time.Second),
		},
		Static: vango.StaticConfig{
			Dir:    "public",
			Prefix: "/",
		},
		DevMode: os.Getenv("VANGO_DEV") == "1",
	})
	if err != nil {
		log.Fatal(err)
	}

	routes.Register(app)

	if err := app.Run(context.Background(), ":3000"); err != nil {
		log.Fatal(err)
	}
}
```

At startup, you typically:

* load config
* build dependencies
* create the Vango app
* wire auth/session hooks if needed
* register routes
* run the server

---

### 37. File-based routing

Vango scans `app/routes`.

Typical mappings:

* `app/routes/index.go` -> `/`
* `app/routes/about.go` -> `/about`
* `app/routes/projects/index.go` -> `/projects`
* `app/routes/projects/id_/index.go` -> `/projects/:id`
* `app/routes/docs/path___/index.go` -> `/docs/*path`
* `app/routes/layout.go` -> root layout
* `app/routes/api/*.go` -> API endpoints

Prefer Go-friendly parameter directories like `id_` and `path___`.

Avoid bracketed directory names in imports.

Vango routes on canonical escaped paths.

For app code, the important rule is simple:

* do not build assumptions on raw undecoded path strings
* use typed route params for path structure
* use URL params for shareable UI state

---

### 38. Page handler signatures

Page handlers should stay thin.

Supported forms:

```go
func Page(ctx vango.Ctx) *vango.VNode
func Page(ctx vango.Ctx, p Params) *vango.VNode
```

Named variants like `IndexPage` or `ShowPage` are also supported by the router.

Thin-handler rule:

* parse params
* choose the page component
* return the view

Do not put blocking I/O in route handlers.

Push stateful logic into Setup components.

Example:

```go
type Params struct {
	ID int `param:"id"`
}

func Page(ctx vango.Ctx, p Params) *vango.VNode {
	return Fragment(ProjectPage(ProjectPageProps{ProjectID: p.ID}))
}
```

---

### 39. Params

Typed params come from struct tags:

```go
type Params struct {
	ID int `param:"id"`
}
```

Let the router reject invalid params.

Do not accept invalid strings and then partially handle them in the page.

Use route params for:

* IDs
* slugs
* UUIDs
* catch-all segments

Use URL params for:

* filters
* sort order
* shareable view state

Path handling has a few security-relevant details:

* non-canonical paths may redirect before your handler runs
* encoded separators are not treated the same as ordinary characters
* catch-all params are the only place where slash-like decoded values should be expected

If you need exact encoded path bytes, use the request URL directly rather than assuming `ctx.Path()` preserves every escape verbatim.

---

### 40. Layouts

Layouts are pure view wrappers:

```go
func Layout(ctx vango.Ctx, children vango.Slot) *vango.VNode
```

Good layout responsibilities:

* shell structure
* navigation
* asset tags
* persistent banners
* script injection

If a layout needs state:

* embed a Setup component inside the layout
* keep the layout function itself pure

Typical root layout:

```go
func Layout(ctx vango.Ctx, children vango.Slot) *vango.VNode {
	return Html(
		Head(
			Meta(Charset("utf-8")),
			Meta(Name("viewport"), Content("width=device-width, initial-scale=1")),
			LinkEl(Rel("stylesheet"), Href(ctx.Asset("styles.css"))),
		),
		Body(
			Main(children),
			VangoScripts(),
		),
	)
}
```

---

### 41. Navigation

Declarative navigation:

```go
Link("/projects", Text("Projects"))
NavLink(ctx, "/settings", Text("Settings"))
```

Programmatic navigation:

```go
ctx.Navigate("/projects")
ctx.Navigate("/projects", vango.WithReplace())
ctx.Navigate("/projects", vango.WithNavigateParams(map[string]any{
	"tab": "activity",
}))
```

Server redirects:

```go
ctx.Redirect("/login", http.StatusSeeOther)
ctx.RedirectExternal("https://example.com", http.StatusFound)
```

Use navigation, not manual client-side hacks.

---

### 42. API routes

API files live under `app/routes/api`.

Supported naming:

* bare verbs: `GET`, `POST`, `PUT`, `PATCH`, `DELETE`
* prefixed verbs: `HealthGET`, `ProjectPOST`

Do not export both styles for the same method in the same file.

Typical signatures:

```go
func GET(ctx vango.Ctx) (T, error)
func GET(ctx vango.Ctx, p Params) (T, error)
func POST(ctx vango.Ctx, in Body) (T, error)
func POST(ctx vango.Ctx, p Params, in Body) (T, error)
```

Return plain typed values when status metadata is unnecessary.

Return `*vango.Response[T]` when you need explicit status codes or response metadata.

```go
func POST(ctx vango.Ctx, in CreateProjectInput) (*vango.Response[Project], error) {
	project, err := createProject(ctx.StdContext(), in)
	if err != nil {
		return nil, vango.BadRequest(err)
	}
	return vango.Created(project), nil
}
```

Useful response helpers:

* `vango.OK(...)`
* `vango.Created(...)`
* `vango.Accepted(...)`
* `vango.NoContent[...]()`
* `vango.Paginated(...)`

Useful HTTP error helpers:

* `vango.BadRequest(...)`
* `vango.Unauthorized(...)`
* `vango.Forbidden(...)`
* `vango.NotFound(...)`
* `vango.Conflict(...)`
* `vango.UnprocessableEntity(...)`
* `vango.InternalError(...)`

---

## Part VI — Forms, Validation, Auth, and User Flows

### 43. Forms

Vango forms should work in two modes:

* interactive live mode
* plain HTTP fallback mode

Server validation is authoritative in both.

The two standard patterns are:

* manual signals + Action
* `setup.Form`

Use `setup.Form` for most real forms.

---

### 44. Manual form pattern

```go
name := setup.Signal(&s, "")
email := setup.Signal(&s, "")

submit := setup.Action(&s,
	func(ctx context.Context, in ContactInput) (struct{}, error) {
		return struct{}{}, sendContact(ctx, in)
	},
	vango.DropWhileRunning(),
)

return func() *vango.VNode {
	return Form(
		OnSubmit(vango.PreventDefault(func() {
			submit.Run(ContactInput{
				Name:  name.Get(),
				Email: email.Get(),
			})
		})),
		Input(Name("name"), Value(name.Get()), OnInput(name.Set)),
		Input(Name("email"), Value(email.Get()), OnInput(email.Set)),
		Button(Type("submit"), Text("Send")),
	)
}
```

This is appropriate when:

* the form is small
* custom behavior dominates
* struct-tag validation is not buying much

---

### 45. Typed forms with `setup.Form`

```go
type ContactFormData struct {
	Name    string `form:"name" validate:"required,min=2,max=100"`
	Email   string `form:"email" validate:"required,email"`
	Message string `form:"message" validate:"required,max=1000"`
}

form := setup.Form(&s, ContactFormData{})
```

Render fields:

```go
return Form(
	OnSubmit(vango.PreventDefault(func() {
		if form.Validate() {
			submit.Run(form.Values())
		}
	})),
	form.Field("name", Input(Type("text"))),
	form.Field("email", Input(Type("email"))),
	form.Field("message", Textarea()),
)
```

Useful form methods:

* `Field(...)`
* `Values()`
* `Validate()`
* `ValidateField(...)`
* `Errors()`
* `FieldErrors(...)`
* `SetError(...)`
* `SetParseError(...)`
* `Reset()`
* `Array(...)`
* `ArrayKeyed(...)`

Built-in validators include:

* `Required`
* `Email`
* `Min`
* `Max`
* `Between`
* `MinLength`
* `MaxLength`
* `Pattern`
* `Alpha`
* `AlphaNumeric`
* `Numeric`
* `UUID`
* `URL`
* `Phone`
* `Past`
* `Future`
* `DateBefore`
* `DateAfter`
* `EqualTo`
* `NotEqualTo`
* `Positive`
* `NonNegative`
* `Custom`
* `Async`

Rules:

* validators must be pure
* parse errors are distinct from validation errors
* database-backed validation belongs in Actions, not validators

---

### 46. Dynamic form arrays

Use `ArrayKeyed` for reorderable or filterable arrays.

```go
type LineItem struct {
	ID   string `form:"id"`
	Name string `form:"name" validate:"required"`
	Qty  int    `form:"qty" validate:"min=1"`
}

form.ArrayKeyed("items",
	func(item any) any { return item.(LineItem).ID },
	func(item form.FormArrayItem, index int) *vango.VNode {
		return Div(
			item.Field("name", Input(Type("text"))),
			item.Field("qty", Input(Type("number"))),
			Button(Type("button"), OnClick(item.Remove()), Text("Remove")),
		)
	},
)
```

Use `Array` only when the order is static and identity does not matter.

---

### 47. HTTP fallback form parsing

For plain HTTP form posts:

```go
var data ContactFormData
if err := vango.ParseFormWithLimit(r, &data, 1<<20); err != nil {
	// handle parse errors
}
```

If you already have a `vango.FormData` value:

```go
var data ContactFormData
_ = vango.ParseFormData(formData, &data)
```

Keep the same server-side validation rules for both live and fallback paths.

---

### 48. Auth model

Vango auth has two layers:

1. HTTP layer validates the request
2. session layer carries the auth projection during live interaction

Do not assume live renders have fresh cookies or headers available.

The standard pattern is:

* HTTP middleware authenticates the request
* request context carries the user or principal
* `OnSessionStart` and `OnSessionResume` rehydrate session auth state
* component code reads auth through `pkg/auth`

---

### 49. The auth bridge

HTTP middleware:

```go
func AuthHTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := validateRequest(r)
		if err == nil && user != nil {
			r = r.WithContext(vango.WithUser(r.Context(), user))
		}
		next.ServeHTTP(w, r)
	})
}
```

Session bridge:

```go
cfg := vango.Config{
	OnSessionStart: func(httpCtx context.Context, s *vango.Session) {
		if user, ok := vango.UserFromContext(httpCtx); ok {
			auth.Set(s, user)
		}
	},
	OnSessionResume: func(httpCtx context.Context, s *vango.Session) error {
		user, err := validateFromContext(httpCtx)
		if err != nil {
			return err
		}
		if user != nil {
			auth.Set(s, user)
		}
		return nil
	},
}
```

Read auth in app code:

```go
user, ok := auth.Get[*User](ctx)
principal, ok := auth.Get[auth.Principal](ctx)
```

Use `auth.Require[...]` when the flow must fail closed.

---

### 50. Route auth middleware

Use `pkg/authmw` for route-level protection.

```go
func Middleware() []vango.RouteMiddleware {
	return []vango.RouteMiddleware{
		authmw.RequireAuth,
		authmw.RequireRole(func(u *User) bool {
			return u.IsAdmin
		}),
	}
}
```

Also available:

* `authmw.RequirePermission(...)`
* `authmw.RequireAny(...)`
* `authmw.RequireAll(...)`

Keep authorization near the route boundary unless the permission check is truly domain-specific to a deeper service.

---

### 51. Session-first auth provider

`pkg/auth/sessionauth` provides a session-first provider adapter.

Typical flow:

```go
provider := sessionauth.New(store)
appHandler := provider.Middleware()(app)
```

Useful pieces:

* `provider.Middleware()`
* `provider.Principal(...)`
* `provider.Verify(...)`
* `sessionauth.SessionFromContext(...)`

Use it when your app already has a session store and you want a straightforward Vango auth integration.

---

### 52. Auth freshness

Long-lived sessions need freshness checks.

Use periodic checks with `AuthCheck` in session config.

Use `ctx.RevalidateAuth()` for sensitive actions right before the mutation.

If the auth projection expires or becomes invalid:

* fail closed for sensitive paths
* reload into the HTTP auth pipeline when appropriate

Do not store raw tokens or provider credentials in persisted app state.

---

### 53. Uploads

Uploads are intentionally handled via HTTP, not via the live WebSocket event channel.

Recommended mounting:

```go
store, err := upload.NewDiskStore("/tmp/uploads", 10*1024*1024)
if err != nil {
	log.Fatal(err)
}

app.HandleUpload("/api/upload", store)
```

Or:

```go
app.HandleUploadWithConfig("/api/upload", store, &upload.Config{
	MaxFileSize:  5 * 1024 * 1024,
	AllowedTypes: []string{"image/png", "image/jpeg"},
})
```

Claim uploaded files later:

```go
file, err := upload.Claim(store, tempID)
```

Rules:

* keep upload endpoints behind CSRF protection
* enforce size and type limits
* treat original filenames as untrusted
* store temp uploads privately
* use the two-phase pattern: upload first, claim later

---

### 54. Toasts

Use `pkg/toast` for live feedback.

Examples:

```go
toast.Success(ctx, "Saved")
toast.Error(ctx, "Could not save")
toast.Warning(ctx, "This action cannot be undone")
toast.Info(ctx, "Background sync complete")
toast.WithTitle(ctx, toast.TypeSuccess, "Settings", "Your changes have been saved.")
```

Toast events are emitted through:

* `ctx.Emit("vango:toast", ...)`

That means your client-side toast UI is your choice.

---

## Part VII — Client Boundaries

### 55. Use the smallest capability that fits

Order of preference:

1. server-driven UI
2. hook
3. island
4. WASM

That is not ideology.

It is a correctness and maintenance gradient.

The more client ownership you introduce:

* the more trust boundaries you create
* the more sync assumptions you own
* the more client packaging and security complexity you add

---

### 56. Hooks

Hooks attach client behavior to server-owned DOM.

Canonical wiring:

```go
Ul(
	Hook("Sortable", map[string]any{
		"handle": ".drag-handle",
	}),
	OnEvent("reorder", func(e vango.HookEvent) {
		from := e.Int("fromIndex")
		to := e.Int("toIndex")
		reorder(from, to)
	}),
)
```

Use hooks for:

* drag and drop
* focus traps
* dropdown behavior
* tooltips
* high-frequency interaction polish

Do not use hooks when a third-party library needs to own a subtree of DOM.

Event payload helpers on `HookEvent` include:

* `String(...)`
* `Int(...)`
* `Float(...)`
* `Bool(...)`
* `Strings(...)`
* `Raw()`
* `Revert()`

Validate payloads as untrusted input.

Use `vango.HookSchemaValidator[T](...)` when typed validation improves safety.

Hook config should be deterministic and serializable.

Hook event names and IDs should stay conservative:

* use simple ASCII identifiers
* keep payload shapes bounded
* do not treat client-provided HIDs or indices as trusted

Hook lifecycle should be treated as:

* mounted
* updated
* destroyed

`updated` may happen frequently. Hook code should therefore be idempotent and cheap.

Hooks may manage behavior and bounded ephemeral DOM state.

Hooks must not permanently take ownership of server-managed structure. If a library needs subtree ownership, use an island instead.

For high-frequency or reorder-driven hook UIs:

* keep payloads conservative
* validate indices and IDs
* keep list identity stable with `RangeKeyed`

---

### 57. Islands

Use islands when a client runtime must own the DOM subtree.

```go
Div(
	JSIsland("rich-editor", map[string]any{
		"content": content.Get(),
	}),
	OnIslandMessage(func(msg vango.IslandMessage) {
		// validate and handle
	}),
)
```

Rules:

* island props must encode to a JSON object
* `nil` props are treated as `{}`
* Vango does not patch inside the island boundary
* communication is message-based

Multi-instance rule:

* the island name may appear multiple times on a page
* each rendered boundary has its own `HID`
* `HID` is the instance address for inbound messages and targeted outbound sends

If you render islands inside dynamic lists, stable list identity is mandatory so the correct island instance keeps the correct boundary identity across reorder and filtering.

Useful server->client helpers:

* `SendToIsland(...)`
* `SendToIslandHID(...)`

Use `vango.IslandSchemaValidator[T](...)` when message payload validation should be typed.

Module loading should stay same-origin by default.

If you allow trusted cross-origin modules, keep the allowlist explicit with script options rather than baking remote origins into random component code.

---

### 58. WASM boundaries

Use WASM when the component really needs client-local execution.

```go
Div(
	WASMComponent("canvas", map[string]any{
		"tool": "pen",
	}),
	OnWASMMessage(func(msg vango.WasmMessage) {
		// validate and handle
	}),
)
```

Rules mirror islands:

* props must encode to a JSON object
* communication is message-based
* use WASM only when latency or offline requirements justify it

Like islands:

* the WASM name may appear multiple times
* each instance gets its own `HID`
* targeted server messages should use the instance `HID` when you mean one mounted boundary

If you render WASM boundaries inside keyed lists, preserve stable list identity so the correct instance keeps the correct runtime boundary.

Useful server->client helpers:

* `SendToWASM(...)`
* `SendToWASMHID(...)`

Use `vango.WasmSchemaValidator[T](...)` for typed payload validation.

---

### 59. Standard hook wrappers

Most `pkg/features/*` packages are compatibility surfaces.

The common practical exception is:

* `github.com/vango-go/vango/pkg/features/hooks/standard`

It provides built-in hook wrappers like:

* `Sortable`
* `Draggable`
* `Dropdown`
* `Tooltip`

You may use it when you want the predefined wrapper, but the canonical guide-level pattern remains explicit `Hook(...)` plus `OnEvent(...)`.

---

## Part VIII — Persistence and Deploy Safety

### 60. What persists

Persisted app state in Vango v1 is:

* `setup.SharedSignal`
* `setup.GlobalSignal`
* `vango.SessionKey[T]`

Not persisted:

* `setup.Signal`
* `setup.Memo`
* `setup.Resource`
* `setup.Action`
* effect lifecycle
* props
* runtime request/session scratch data

Persist durable UX state intentionally.

Do not persist everything just because you can.

---

### 61. Persisted initializers

Persisted initializers must be deterministic.

Allowed:

* literals
* constant expressions
* simple deterministic construction

Forbidden:

* props
* environment values
* time
* random IDs
* I/O
* user-defined helper calls

For persisted state, prefer the strict rule:

* literals
* constant expressions
* basic composite construction
* simple built-in operations only

If an initializer needs runtime context, it is almost certainly the wrong persisted shape.

Good:

```go
prefs := setup.SharedSignal(&s, UserPrefs{
	Theme: "system",
})
```

Bad:

```go
prefs := setup.SharedSignal(&s, UserPrefs{
	Theme: s.Props().Peek().Theme,
})
```

Persist references and then load real data through Resources or Actions.

---

### 62. Schema IDs

If a persisted type should survive renames and moves, give it a stable schema ID:

```go
func (UserPrefs) VangoSchemaID() string {
	return "myapp:UserPrefs:v1"
}
```

Changing that string is a compatibility change.

Do it deliberately.

---

### 63. Warm vs cold deploy

Warm deploy:

* persisted schema stays compatible
* sessions resume

Cold deploy:

* persisted schema breaks compatibility
* sessions refresh instead of resuming

Typical warm changes:

* adding new persisted state
* safe refactors where tooling preserves continuity

Typical cold changes:

* removing persisted state
* incompatible persisted type changes
* schema ID changes

Refactor workflow matters here:

* additive persisted state is usually warm
* alias-preserved moves and renames can still be warm
* removed state or changed persisted fingerprints are cold

When in doubt, read the plan output literally and review the generated artifacts rather than guessing.

Treat the distinction as part of normal code review.

---

### 64. Generated artifacts

Commit these:

* `vango_state_manifest.json`
* `vango_state_schema.json`
* generated `vango_state_bindings_gen.go` files

You do not hand-edit them.

You review them.

The normal workflow is:

* edit code
* run `vango dev` or the state commands
* commit code plus generated artifacts

---

### 65. Persistence errors and budgets

Persisted writes can fail.

Common causes:

* per-value budget exceeded
* per-session budget exceeded
* storage errors
* codec errors

Use `s.OnPersistError(...)` when you want app-level UI handling:

```go
s.OnPersistError(func(err *vango.PersistBudgetError) {
	errorText.Set(err.Error())
})
```

If you hit budgets routinely, redesign the state shape.

Do not just raise budgets because the bad shape "works."

---

## Part IX — Security and Operations

### 66. WebSocket origins

In production, configure origin checks.

Use:

* explicit allowed origins
* or same-origin enforcement

Do not accept arbitrary origins.

---

### 67. CSRF

If you use cookie auth or any stateful HTTP endpoints:

* enable CSRF protection
* include the CSRF token on protected form and upload requests

This matters especially for:

* uploads
* login/logout
* fallback HTTP form posts
* any side-effecting HTTP API

---

### 68. Cookies

Production cookie defaults should usually be:

* `Secure`
* `HttpOnly`
* `SameSite=Lax` or stricter

Use `ctx.SetCookieStrict(...)` when you want framework-enforced policy on response cookies.

---

### 69. Trusted proxies

If you run behind a load balancer or reverse proxy, configure trusted proxies correctly.

This affects:

* scheme detection
* real client IP
* session-per-IP limits
* origin logic

If trusted proxy config is wrong, you can collapse all traffic into one apparent IP and effectively rate-limit your own app.

Reverse proxies must also preserve WebSocket behavior correctly:

* pass upgrade headers
* allow long-lived idle WS connections
* forward scheme and client IP correctly
* route `/_vango/*` to the Vango handler
* preserve the live mount `?path=` query contract

If reconnects or handler mismatches appear only behind a proxy, inspect this before changing app code.

---

### 70. XSS and URL sanitization

Vango escapes text content by default.

URL-bearing attributes are also sanitized by policy.

Still:

* treat raw HTML as dangerous
* validate user-supplied URLs where the domain matters
* do not disable raw HTML protections casually

Prefer typed Vango views over raw HTML whenever possible.

---

### 71. CSP

Vango applies a secure CSP by default.

Keep these in mind:

* `connect-src` must allow your WebSocket endpoint
* inline scripts are not required for normal Vango operation
* if you allow inline scripts, you are weakening XSS protection

Use a hardened CSP posture when your app does not need looser defaults.

---

### 72. Scaling

Stateful server-driven apps scale around:

* active sessions
* detached sessions
* resume windows
* persistence store
* broadcast backend for global state

Decide which durability level you want:

* no resume
* in-memory resume
* persistent session resume

For multi-instance deployments:

* configure a real session store if you need cross-instance resume
* configure broadcast if global signals must fan out live

---

### 73. Observability

At minimum, observe:

* active sessions
* detached sessions
* resume success/failure
* patch bytes
* patch mismatches
* resource latency and errors
* action latency and errors
* schema mismatches
* persisted write rejections

Also:

* propagate `ctx.StdContext()` into DB and service calls
* use structured logs
* avoid logging secrets or raw PII

---

## Part X — Testing and Tooling

### 74. `vtest`

Use `pkg/vtest` for deterministic server-side testing.

Basic flow:

```go
h := vtest.New(t)
m := vtest.Mount(h, Component, Props{
	Title: "Example",
})
```

Useful methods:

* `HTML(...)`
* `AssertSnapshot(...)`
* `ExistsByTestID(...)`
* `TextByTestID(...)`
* `AssertTextByTestID(...)`
* `ClickByTestID(...)`
* `InputByTestID(...)`
* `SubmitByTestID(...)`
* `AwaitResource(...)`
* `AwaitAction(...)`
* `AssertActionState(...)`

Prefer `data-testid` queries over brittle selector logic.

---

### 75. Snapshot testing

Snapshot convention:

* `testdata/vango-snapshots/{TestName}/{snapshot}.html`

Use snapshots for:

* structural output
* major visual state transitions
* route-level page output

Use targeted assertions for:

* counts
* error text
* action state
* specific critical fields

---

### 76. Async testing

Do not sleep.

Use:

* `AwaitResource(...)`
* `AwaitAction(...)`

Inject deterministic fake services into Resources and Actions.

Keep external I/O out of unit tests.

---

### 77. CLI workflow

Core commands:

* `vango create`
* `vango dev`
* `vango build`
* `vango test`
* `vango gen routes`
* `vango gen bindings`
* `vango state plan`
* `vango state apply`
* `vango test schema`
* `vango state impact`
* `vango inspect state --json`
* `vango lint`

Normal loop:

1. edit code
2. run `vango dev`
3. commit code plus generated artifacts

CI should verify:

* routes are up to date
* bindings are up to date
* state artifacts are up to date
* warm vs cold deploy impact is explicit

---

### 78. Diagnostics

Vango diagnostics should be machine-readable and stable.

Expect error codes around:

* conditional allocation
* render allocation
* missing list keys
* stale generated artifacts
* schema compatibility

Read them literally.

They are not advisory prose.

They are telling you which contract you broke.

---

### 79. Troubleshooting

Common failure patterns are mechanical once you know what they map to.

`conditional allocation`

* cause: primitive allocation behind runtime control flow
* fix: allocate unconditionally in Setup and branch only in render

`render allocation`

* cause: calling `setup.*` inside the render closure
* fix: move allocation to Setup

`stale bindings or manifest`

* cause: source changed without regenerated artifacts
* fix: run the state and generation commands, then commit the generated files

`schema mismatch after deploy`

* cause: incompatible persisted state change
* fix: accept and acknowledge cold deploy, or restore compatibility

`patch mismatch or self-heal reloads`

* cause: unstable list identity, DOM ownership conflict, or SSR/WS route mismatch
* fix: inspect list keys first, then client boundaries, then proxy and mount path handling

`persist budget failures`

* cause: persisted state shape is too large
* fix: persist references instead of large blobs

When debugging, start with:

* the exact diagnostic code
* recent list or client-boundary changes
* recent persisted type or schema changes
* recent proxy or deployment changes

---

## Part XI — Secondary and Legacy Surfaces

### 80. `vango.CreateContext`

Vango ships a root-level typed context API:

```go
var ThemeContext = vango.CreateContext("light")
```

It works.

It is supported.

It is **not** the first thing to reach for.

Prefer:

* explicit props
* layout composition
* SessionKey or SharedSignal for real shared state

Use context when it clearly reduces noise without obscuring ownership.

---

### 81. `pkg/pref`

The preference package exists for sync-oriented preference flows.

It is not the primary Vango state model.

Prefer SessionKey or SharedSignal unless you specifically need:

* local vs remote merge policies
* conflict handling
* special preference sync semantics

---

### 82. `pkg/features/*`

Most `pkg/features/*` packages are compatibility layers around the current preferred surface.

Examples:

* `pkg/features/form` -> prefer `setup.Form`
* `pkg/features/hooks` -> prefer `Hook` and `OnEvent`
* `pkg/features/islands` -> prefer `JSIsland`
* `pkg/features/wasm` -> prefer `WASMComponent`

The standard hook wrappers under `pkg/features/hooks/standard` are still fine to use when you want those built-ins.

---

### 83. `vango.Func` and older stateful styles

The root package still exposes lower-level helpers such as `vango.Func`.

Do not use them for new stateful components.

For new app code:

* always use `vango.Setup`
* allocate reactive primitives with `setup.*`
* keep render pure

---

## Appendix A — Quick Checklists

### A.1 New component checklist

* use `vango.Setup`
* use named props
* use `vango.Slot` for children
* allocate state only in Setup
* keep render pure
* no blocking I/O
* no goroutines

### A.2 New route checklist

* keep handler thin
* parse params with typed `Params`
* move stateful logic into a Setup component
* use `ctx.StdContext()` in services and DB calls
* commit regenerated routes

### A.3 New async flow checklist

* use Resource for reads
* use Action for writes
* choose a concurrency policy
* render loading/error/success explicitly
* do not write signals from worker code

### A.4 New persisted state checklist

* is persistence really needed?
* can you persist a reference instead of the full object?
* should this be SharedSignal, GlobalSignal, or SessionKey?
* is the initializer deterministic?
* does the type need a stable `VangoSchemaID()`?
* did you review warm vs cold deploy impact?

### A.5 New client boundary checklist

* can this stay server-driven?
* if not, is a hook enough?
* if not, does it need an island?
* if not, is WASM truly justified?
* are all client payloads validated server-side?
* is DOM ownership explicit?

## Appendix B — The Shortest Possible Rule Set

If you are a coding agent and need the absolute minimum operating rules:

1. Use `vango.Setup` for every stateful component.
2. Allocate all reactive primitives in Setup, unconditionally.
3. Never allocate primitives in render.
4. Never do blocking I/O in Setup, render, or event handlers.
5. Use Resources for reads and Actions for writes.
6. Keep all signal writes on the session loop.
7. Use `RangeKeyed` for dynamic lists.
8. Use `vango.Slot` for children in stateful components.
9. Persist only small durable UX state.
10. Use SessionKey, SharedSignal, or GlobalSignal for persistence.
11. Keep route handlers thin.
12. Use `setup.Form` for most forms.
13. Use `setup.URLParam` for shareable URL state.
14. Prefer server-driven UI; use hooks, islands, or WASM only when needed.
15. Commit generated routes and state artifacts.

## Appendix C — App-Builder API Inventory

This appendix is intentionally inventory-style.

It exists so a developer or coding agent can quickly confirm:

* which packages are primary
* which helpers are normal
* which helpers are secondary
* which APIs belong to route code, component code, or operations code

This is not a signature dump of every exported symbol in the repository.

It is the practical app-building inventory.

### C.1 Root package `vango`

Primary types you will touch often:

* `App`
* `Config`
* `Ctx`
* `SetupCtx[P]`
* `RenderFn`
* `Signal[T]`
* `Memo[T]`
* `Resource[T]`
* `Action[A, R]`
* `SessionKey[T]`
* `Slot`
* `VNode`
* `NoProps`
* `Response[T]`
* `PagedResponse[T]`

Primary constructors and helpers:

* `Setup(...)`
* `New(...)`
* `MustNew(...)`
* `NewSessionKey(...)`
* `Default(...)`
* `UseCtx()`
* `Children(...)`
* `PreventDefault(...)`
* `Tx(...)`
* `TxNamed(...)`
* `Timeout(...)`
* `Interval(...)`
* `Subscribe(...)`
* `GoLatest(...)`
* `UntrackedGet(...)`

Action-state matchers:

* `OnActionIdle(...)`
* `OnActionRunning(...)`
* `OnActionSuccess(...)`
* `OnActionError(...)`

Resource-state matchers:

* `OnPending(...)`
* `OnLoading(...)`
* `OnLoadingOrPending(...)`
* `OnReady(...)`
* `OnError(...)`
* `OnResourceSuccess(...)`
* `OnResourceError(...)`

Action concurrency options:

* `CancelLatest()`
* `DropWhileRunning()`
* `Queue(n)`

Response helpers:

* `OK(...)`
* `Created(...)`
* `Accepted(...)`
* `NoContent[T]()`
* `Paginated(...)`

HTTP error helpers:

* `BadRequest(...)`
* `BadRequestf(...)`
* `Unauthorized(...)`
* `Forbidden(...)`
* `NotFound(...)`
* `Conflict(...)`
* `UnprocessableEntity(...)`
* `InternalError(...)`
* `ServiceUnavailable(...)`

Form parsing helpers:

* `ParseForm(...)`
* `ParseFormWithLimit(...)`
* `ParseFormData(...)`
* `NewFormData(...)`
* `NewFormDataFromSingle(...)`

Validation helpers re-exported from the form surface:

* `Required()`
* `Email()`
* `Min(...)`
* `Max(...)`
* `Between(...)`
* `MinLength(...)`
* `MaxLength(...)`
* `Pattern(...)`
* `Alpha()`
* `AlphaNumeric()`
* `Numeric()`
* `UUID()`
* `URL()`
* `Phone()`
* `Past()`
* `Future()`
* `DateBefore(...)`
* `DateAfter(...)`
* `EqualTo(...)`
* `NotEqualTo(...)`
* `Positive()`
* `NonNegative()`
* `Custom(...)`
* `Async(...)`

Navigation helpers:

* `WithReplace()`
* `WithNavigateParams(...)`
* `WithoutScroll()`

Auth-bridge helpers on the root package:

* `WithUser(...)`
* `UserFromContext(...)`

Cookie helper on the root package:

* `WithCookieHTTPOnly(...)`

Observability and rate helpers you may see in app code:

* `ResumeWindow(...)`
* `RetryOnError(...)`
* `StaleTime(...)`
* `GlobalSignalRefreshInterval(...)`
* `DisableGlobalSignalRefresh()`

Schema validators for client-boundary payloads:

* `HookSchemaValidator[T](...)`
* `IslandSchemaValidator[T](...)`
* `WasmSchemaValidator[T](...)`

### C.2 `vango.Ctx`

Request and routing:

* `Request()`
* `Path()`
* `Method()`
* `Query()`
* `QueryParam(key)`
* `Param(key)`
* `Header(key)`
* `Cookie(name)`

Response and navigation:

* `Status(code)`
* `SetHeader(key, value)`
* `SetCookie(cookie)`
* `SetCookieStrict(cookie, opts...)`
* `Redirect(path, code)`
* `RedirectExternal(url, code)`
* `Navigate(path, opts...)`

Session and auth:

* `Session()`
* `AuthSession()`
* `User()`
* `SetUser(user)`
* `Principal()`
* `MustPrincipal()`
* `RevalidateAuth()`
* `BroadcastAuthLogout()`

Async and integration:

* `Dispatch(fn)`
* `StdContext()`
* `Done()`
* `Emit(name, data)`

Request-scoped values:

* `SetValue(key, value)`
* `Value(key)`

Operational helpers:

* `Logger()`
* `Asset(path)`
* `StormBudget()`
* `Mode()`
* `PatchCount()`
* `AddPatchCount(...)`

Guidance:

* route handlers use `ctx` directly
* Setup code reaches `ctx` through `s.Ctx()`
* render code reaches `ctx` through `vango.UseCtx()`
* off-loop integrations use `ctx.Dispatch(...)`

### C.3 `setup` package

State allocation:

* `Signal(...)`
* `Memo(...)`
* `SharedSignal(...)`
* `GlobalSignal(...)`

Async work:

* `Resource(...)`
* `ResourceKeyed(...)`
* `Action(...)`

State orchestration:

* `OnChange(...)`

Feature helpers:

* `Form(...)`
* `URLParam(...)`

The rule for `setup` is simple:

* if it allocates reactive state for app code, it should probably come from `setup`

### C.4 `SetupCtx`

Available methods:

* `Props()`
* `Ctx()`
* `OnMount(...)`
* `Effect(...)`
* `OnChange(...)`
* `OnPersistError(...)`

Use patterns:

* `Props()` for reactive props access
* `Ctx()` for SessionKey access or route/runtime data during Setup
* `OnMount(...)` for post-commit setup
* `Effect(...)` for reactive side effects
* `OnChange(...)` for explicit orchestration
* `OnPersistError(...)` for persistence failure UX

### C.5 `Signal[T]`

Common read/write methods:

* `Get()`
* `Peek()`
* `Set(v)`
* `Update(fn)`

Common numeric helpers:

* `Inc()`
* `Dec()`
* `Add(v)`
* `Sub(v)`
* `Mul(v)`
* `Div(v)`

Common boolean helpers:

* `SetTrue()`
* `SetFalse()`
* `Toggle()`

Common slice/container helpers:

* `Append(...)`
* `Prepend(...)`
* `InsertAt(...)`
* `RemoveAt(...)`
* `RemoveFirst()`
* `RemoveLast()`
* `RemoveWhere(...)`
* `SetAt(...)`
* `UpdateAt(...)`
* `UpdateWhere(...)`
* `Len()`
* `Clear()`

Map-style helpers on map-backed signals:

* `SetKey(...)`
* `GetKey(...)`
* `UpdateKey(...)`
* `RemoveKey(...)`
* `HasKey(...)`
* `Keys()`
* `Values()`

Metadata and inspection helpers exist, but most app code should not need them.

### C.6 `Resource[T]`

Common methods:

* `State()`
* `Data()`
* `DataOr(...)`
* `Error()`
* `Fetch()`
* `Refetch()`
* `Invalidate()`
* `Mutate(...)`
* `Match(...)`
* `IsLoading()`
* `IsReady()`
* `IsError()`

Options you may use:

* `StaleTime(...)`
* `RetryOnError(...)`

Mental model:

* Resource is view-facing async read state
* Resource state should be rendered explicitly

### C.7 `Action[A, R]`

Common methods:

* `Run(arg)`
* `State()`
* `Result()`
* `Error()`
* `Reset()`
* `Match(...)`
* `IsIdle()`
* `IsRunning()`
* `IsSuccess()`
* `IsError()`

Related hooks and options:

* `CancelLatest()`
* `DropWhileRunning()`
* `Queue(n)`
* `OnActionRejected(...)`
* `ActionTxName(...)`
* `ActionOnStart(...)`
* `ActionOnSuccess(...)`
* `ActionOnError(...)`

Mental model:

* Action is view-facing async mutation state
* `Run(...)` happens on the session loop
* work runs off-loop

### C.8 `el` package categories

Common HTML constructors:

* `Html`
* `Head`
* `Body`
* `Main`
* `Header`
* `Footer`
* `Nav`
* `Section`
* `Article`
* `Aside`
* `Div`
* `Span`
* `P`
* `H1` through `H6`
* `Button`
* `Input`
* `Textarea`
* `Select`
* `Option`
* `Label`
* `Form`
* `Table`
* `Ul`
* `Ol`
* `Li`
* `Img`
* `Link`
* `LinkEl`
* `Script`
* `Meta`
* `Title`
* `TitleEl`

Common SVG constructors:

* `Svg`
* `Path`
* `Circle`
* `Rect`
* `Line`
* `Ellipse`
* `Polygon`
* `Polyline`
* `G`
* `Defs`
* `Use`

Common attributes:

* `Class`
* `Classes`
* `ClassIf`
* `ID`
* `Name`
* `Type`
* `Value`
* `Checked`
* `Selected`
* `Disabled`
* `Placeholder`
* `Href`
* `Src`
* `Action`
* `Method`
* `Role`
* `AriaLabel`
* `AriaHidden`
* `AriaExpanded`
* `Data(...)`
* `DataAttr(...)`

Common flow helpers:

* `Fragment`
* `If`
* `IfElse`
* `Unless`
* `When`
* `Show`
* `ShowWhen`
* `Either`
* `Switch`
* `Case_`
* `Maybe`
* `Nothing`

List helpers:

* `Range`
* `RangeKeyed`
* `RangeMap`
* `Key(...)`

Client-boundary helpers:

* `Hook(...)`
* `OnEvent(...)`
* `JSIsland(...)`
* `OnIslandMessage(...)`
* `SendToIsland(...)`
* `SendToIslandHID(...)`
* `WASMComponent(...)`
* `WASMModule(...)`
* `OnWASMMessage(...)`
* `SendToWASM(...)`
* `SendToWASMHID(...)`
* `VangoScripts(...)`

Security-sensitive helpers:

* `DangerouslySetInnerHTML(...)`
* `SanitizeTrustedHTML(...)`
* `UnsafeTrustedHTML(...)`

Testing helper:

* `TestID(...)`

### C.9 `setup.Form` working set

Creation:

* `setup.Form(&s, initial)`

Read and render:

* `Field(...)`
* `Values()`
* `Get(...)`
* `GetString(...)`
* `GetInt(...)`
* `GetBool(...)`

Validation and error state:

* `Validate()`
* `ValidateField(...)`
* `Errors()`
* `FieldErrors(...)`
* `ParseErrors()`
* `FieldParseError(...)`
* `HasError(...)`
* `ClearErrors()`
* `ClearParseErrors()`
* `SetError(...)`
* `SetParseError(...)`

Dirty/touched/submitting state:

* `IsDirty()`
* `FieldDirty(...)`
* `IsTouched()`
* `IsSubmitting()`
* `SetSubmitting(...)`

Array helpers:

* `Array(...)`
* `ArrayKeyed(...)`
* `ArrayLen(...)`
* `InsertAt(...)`
* `RemoveAt(...)`

Programmatic updates:

* `Set(...)`
* `SetValues(...)`
* `Reset()`
* `AddValidators(...)`

### C.10 Auth package working set

Primary auth helpers:

* `auth.Get[T](ctx)`
* `auth.Require[T](ctx)`
* `auth.Set(session, user)`
* `auth.SetPrincipal(session, principal)`
* `auth.Login(ctx, user)`
* `auth.Logout(ctx)`
* `auth.IsAuthenticated(ctx)`

Common auth middleware:

* `authmw.RequireAuth`
* `authmw.RequireRole(...)`
* `authmw.RequirePermission(...)`
* `authmw.RequireAny(...)`
* `authmw.RequireAll(...)`

Session-first auth provider:

* `sessionauth.New(store)`
* `provider.Middleware()`
* `provider.Principal(ctx)`
* `provider.Verify(ctx, principal)`
* `sessionauth.SessionFromContext(ctx)`

### C.11 Upload package working set

Creation and mounting:

* `upload.NewDiskStore(...)`
* `upload.NewS3Store(...)`
* `app.HandleUpload(...)`
* `app.HandleUploadWithConfig(...)`
* `upload.Handler(...)`
* `upload.HandlerWithConfig(...)`

Using temp uploads:

* `upload.Claim(store, tempID)`

Common errors:

* `upload.ErrNotFound`
* `upload.ErrExpired`
* `upload.ErrTooLarge`
* `upload.ErrTypeNotAllowed`

### C.12 Toast package working set

Server-side helpers:

* `toast.Success(...)`
* `toast.Error(...)`
* `toast.Warning(...)`
* `toast.Info(...)`
* `toast.WithTitle(...)`
* `toast.WithAction(...)`
* `toast.Custom(...)`

Client integration:

* listen for the `"vango:toast"` event

### C.13 `pkg/vtest` working set

Harness creation:

* `vtest.New(t, opts...)`
* `vtest.Mount(...)`

Config options:

* `WithUpdateSnapshots(...)`
* `WithSnapshotDir(...)`
* `WithPrettyHTML(...)`
* `WithDebugMode(...)`
* `WithAwaitMaxSteps(...)`

Queries and assertions:

* `HTML(...)`
* `AssertSnapshot(...)`
* `ExistsByTestID(...)`
* `AssertExistsByTestID(...)`
* `AssertNotExistsByTestID(...)`
* `TextByTestID(...)`
* `AssertTextByTestID(...)`
* `SignalValue(...)`
* `AssertSignal(...)`

Events and async coordination:

* `ClickByTestID(...)`
* `InputByTestID(...)`
* `SubmitByTestID(...)`
* `EmitEventByTestID(...)`
* `EmitEventByHID(...)`
* `AwaitResource(...)`
* `AwaitAction(...)`
* `AssertActionState(...)`

### C.14 Generated-tool workflow inventory

Scaffolding:

* `vango create`
* `vango gen route`
* `vango gen api`
* `vango gen component`
* `vango gen store`
* `vango gen middleware`
* `vango gen schemaid`
* `vango gen openapi`

Core lifecycle:

* `vango dev`
* `vango build`
* `vango test`
* `vango lint`

Routing and state artifacts:

* `vango gen routes`
* `vango gen bindings`
* `vango state plan`
* `vango state apply`
* `vango state impact`
* `vango state alias ...`
* `vango test schema`
* `vango inspect state --json`

### C.15 Preferred package selection map

If you need state:

* local -> `setup.Signal`
* session-shared durable -> `setup.SharedSignal`
* app-shared durable -> `setup.GlobalSignal`
* session durable KV -> `vango.SessionKey[T]`
* derived -> `setup.Memo`

If you need async work:

* read -> `setup.Resource`
* keyed read -> `setup.ResourceKeyed`
* write -> `setup.Action`

If you need form handling:

* most cases -> `setup.Form`
* very custom small case -> manual signals + Action

If you need URL state:

* use `setup.URLParam`

If you need client behavior:

* server-driven if possible
* hook if behavior-only
* island if DOM ownership transfer
* WASM if client execution is the point

If you need testing:

* use `pkg/vtest`
