## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

When the user types `/graphify`, invoke the `skill` tool with `skill: "graphify"` before doing anything else.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- Dirty graphify-out/ files are expected after hooks or incremental updates; dirty graph files are not a reason to skip graphify. Only skip graphify if the task is about stale or incorrect graph output, or the user explicitly says not to use it.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).

<!-- continuity:start -->
## Project continuity (read first)
Several agents on several machines and accounts work on this repo, so intent lives in files, not chat history.
1. At session start run `.continuity/continuity.sh start` and read everything it prints. Before touching code, summarize back to the user in at most 10 lines: what the project is for, the current task, and which protected paths are near it.
2. Stay inside the scope fence in `.continuity/PROGRESS.md`. Never edit `frozen` paths from `.continuity/PROTECTED.md` (ask the user). `stable` paths need an entry in `.continuity/DECISIONS.md` in the same commit. Never edit PROTECTED.md or VISION.md once committed; propose changes in chat.
3. Log non-obvious choices in `DECISIONS.md` as you make them, not at the end.
4. After each finished subtask update "Last known state" in `PROGRESS.md` and commit. Before ending a session follow `.continuity/prompts/handoff.md`.
<!-- continuity:end -->
