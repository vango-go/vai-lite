Design Update Report: “Additive Options” for Tool Bundles (WithTools auto-attaches tool definitions)

  ## 1) Summary

  This update improves SDK ergonomics by eliminating a common footgun: users can accidentally attach a tool definition to a request (req.Tools) but
  forget to register its handler (or register a handler but forget to attach the definition). The proposed change makes WithTools(...) and
  WithToolSet(...) additive:

  - They continue to register tool handlers (current behavior).
  - They also automatically attach the corresponding tool definitions to the request for that execution (new behavior).
  - The SDK deduplicates tools to avoid double-inclusion when users also attach tools explicitly in req.Tools.

  This preserves the “SDK as primitives” philosophy (no Agent/Registry) while making the “safe thing” the easiest thing.

  ———

  ## 2) Current Behavior (Problem)

  ### 2.1 Today’s callsite often looks like this

  t := vai.MakeTool(...)

  req := &vai.MessageRequest{
    Tools: []vai.Tool{t.Tool}, // definition sent to model
  }

  client.Messages.Run(ctx, req, vai.WithTools(t)) // handler registered

  ### 2.2 Footgun scenarios

  1. User attaches definition but forgets handler registration:

  req.Tools = []vai.Tool{t.Tool}
  client.Messages.Run(ctx, req) // tool call happens -> “no handler registered”

  2. User registers handler but forgets to attach definition:

  client.Messages.Run(ctx, req, vai.WithTools(t)) // handler exists but model never sees tool

  Both are common, frustrating mistakes.

  ———

  ## 3) Proposed Design

  ### 3.1 “Additive option” behavior

  Update the option processing so that WithTools(toolWithHandlers...) does:

  - Registers ToolHandler for each tool name (unchanged).
  - Adds each tool definition to an internal cfg.extraTools list to be merged into the request for that run/stream.

  Likewise, WithToolSet(ts):

  - Registers ts.Handlers() (unchanged).
  - Adds ts.Tools() to cfg.extraTools (new).

  ### 3.2 Effective tool list computation

  At each model call (both Run and RunStream), construct:

  effectiveTools = dedupe(req.Tools + cfg.extraTools)

  This ensures:

  - The user can continue to explicitly specify req.Tools.
  - WithTools provides a safe “one-liner” alternative.
  - Duplication is prevented when both are used.

  ### 3.3 Deduplication strategy

  Tool definitions are heterogeneous (function tools, native tools like web search, etc.). Dedup must be correct and predictable.

  Recommended rules:

  - Function tools: dedupe by (Type=function, Name) (name is the stable identity).
  - Native tools (web search, code execution, computer use, etc.):
      - Either dedupe by (Type, JSON(config)) (more complex) or
      - Keep simple: only dedupe function tools, and allow multiple native tools (often harmless, and configs may differ).

  Given your “lite primitives” stance, I recommend:

  - Deduplicate function tools by name (most important / most common).
  - For native tools, do not dedupe unless we have a strong identity definition (or add later if needed).

  ### 3.4 No request mutation

  A key benefit: the request object remains reusable across calls. We do not mutate req.Tools; we only produce an “effective tools slice” for the
  provider call.

  ### 3.5 Preserve escape hatches

  We keep existing lower-level options intact:

  - WithToolHandler(name, fn) / WithToolHandlers(map) remain “register handler only”.
  - WithTools(...) becomes “register + attach definition”.

  So if a user truly wants to register a handler but not expose it this run, they can use WithToolHandler.

  ———

  ## 4) New/Updated Developer Experience

  ### 4.1 One-liner safe path (new recommended)

  search := vai.MakeTool(...)

  req := &vai.MessageRequest{
    Model:    "...",
    Messages: msgs,
  }

  result, err := client.Messages.Run(ctx, req, vai.WithTools(search))

  No req.Tools = ... required.

  ### 4.2 Advanced explicit path (still supported)

  Users can still attach tools explicitly and register handlers explicitly:

  req.Tools = append(req.Tools, search.Tool)
  result, err := client.Messages.Run(ctx, req, vai.WithToolHandler(search.Name, search.Handler))

  ### 4.3 Mix and match (dedupe prevents duplicates)

  req.Tools = []vai.Tool{search.Tool}
  result, err := client.Messages.Run(ctx, req, vai.WithTools(search)) // OK; tool appears once

  ———

  ## 5) API Changes

  ### 5.1 Backward compatibility

  This is not a breaking change for typical users:

  - Existing code that already does both req.Tools + WithTools continues working (dedupe avoids duplicates).
  - Code that does only WithTools will now start working “as expected” (previously model didn’t see tools unless req.Tools also included them).

  The only possible behavior change:

  - If someone relied on WithTools registering handlers but intentionally not exposing the tool to the model, that will change. But that’s better
    served by WithToolHandler(s) anyway, and we can document that distinction.

  ### 5.2 Implementation surface changes

  - runConfig gains extraTools []types.Tool.
  - WithTools / WithToolSet updated to add to extraTools.
  - Run and RunStream request-building updated to merge + dedupe tool definitions per call.

  ———

  ## 6) Interaction With Existing Features

  ### 6.1 WithBuildTurnMessages

  No conflict:

  - WithBuildTurnMessages controls Messages (context).
  - This tool update controls Tools (capabilities).
  - Both apply at the turn boundary.

  ### 6.2 beforeCall hook

  The beforeCall hook currently can edit the request. With additive tools:

  - We should define ordering:
      - Build messages/tools for the turn → then call beforeCall(turnReq).
  - This lets advanced users override/replace tool lists if desired.

  ### 6.3 ToolChoice / tool limits

  No change. The model receives the same tool definitions; execution semantics remain the same.

  ———

  ## 7) Tests and Validation

  ### 7.1 Unit tests (recommended additions)

  - WithTools adds to cfg.extraTools.
  - Effective tool list includes cfg.extraTools even if req.Tools empty.
  - Dedup: tool appears once if it is in both req.Tools and cfg.extraTools.
  - WithToolHandler does not add tool definition.

  ### 7.2 Integration tests (optional)

  - A streaming or non-streaming tool execution test where req.Tools is empty but WithTools(tool) is passed; confirm tool gets called at least
    sometimes (model-dependent, so structure carefully—possibly by using ToolChoiceTool("name") to force the call).

  ———

  ## 8) Documentation Updates

  - README + Developer guide: update examples to show that WithTools(tool) is sufficient; optionally mention:
      - WithTools = “register + attach”
      - WithToolHandler = “register only”

  This will substantially reduce confusion.

  ———

  ## 9) Recommendation

  Adopt the additive option pattern with:

  - Function-tool dedupe by name,
  - No request mutation,
  - Clear docs about the difference between WithTools and WithToolHandler.

  This is a high-impact DX improvement with minimal complexity and preserves the “lite primitives” design.