# Agent Handoff

This repository uses the `/sdlc:*` lifecycle skills with repo-local routing in `agent-control/`.

Before SDLC planning, implementation, review, merge, or closeout work:

- Load `agent-control/bootstrap-context.yaml`.
- Use `agent-control/skill-routing.yaml` to resolve the package skill.
- Use `agent-control/control-planes.yaml` for authority, source-review, issue-layer, and repository conventions.
- Use `agent-control/review-routing.yaml` for review lanes and surface-specific checks.
- Use `agent-control/lifecycle.yaml`, `agent-control/handoff-profile.yaml`, and `agent-control/post-merge.yaml` for checkpoint, handoff, merge-wait, and closeout policy.

Keep `AGENTS.md` as a pointer file. Project-specific SDLC facts belong in `agent-control/`; reusable lifecycle behavior belongs in the installed `sdlc:*` skills.

AI-facing Markdown and YAML that define skills, prompts, routing, tool contracts, or workflow policy are repository source, not docs-only material.
