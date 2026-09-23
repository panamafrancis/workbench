# Hands-off review via the PM

## Problem

Asking the PM to review a set of pull requests created the review tree and
stopped there:

1. Nothing could launch an agent on the PM's behalf. `new_tree` told the PM to
   "tell the human to press enter", and the PM's standing rules forbade opening
   tabs (it is sandboxed and cannot reach zellij anyway).
2. Agents never start a turn on their own. No first message was ever passed to
   the model CLI, so even with a brief waiting the agent sat at an empty prompt.
3. The review tree's `info.md` never mentioned `inbox` (the authoring template
   did), and `message_agent` refused an agent that had not been launched yet, so
   the PM could not pre-seed one either.

## Design

- **Launch queue.** `new_tree start:true` / `start_agent` register the agent,
  deliver the brief to its mailbox, then append a `LaunchRequest` to
  `~/.supatree/pm/launch.jsonl`. The watcher — the only process outside nono —
  drains it every 2s and runs `supatree open <tree> --agent <a> --session <s>
  --background` for each.
- **Focus restore.** `--background` records the focused tab and returns to it
  after opening, replacing the old "never open a tab unprompted" rule.
- **Kickoff prompt.** New `Model.PromptArgs` (`prompt_args`, `{prompt}` token;
  claude defaults to `["{prompt}"]`, backfilled into existing configs).
  `OpenRootAgent` passes the fixed `KickoffPrompt` whenever the agent has mail —
  which also fixes a human opening an agent that has mail waiting.
- **Default review brief.** A review tree started without a brief is told to run
  the full review, write `.supatree/review.md`, post if `outward` allows, and
  report back to `pm`.
- **Reply path.** `message_agent agent=pm` from a tree agent delivers to that
  tree's mailbox; the watcher forwards it into `requests.jsonl`, which the PM
  reads first every turn.
- **Gate.** Starting an agent is a mutation: `auto`, or `asked` below it.

## Not done

- The PM itself is still only woken by a human turn; a forwarded reply waits in
  `requests` until then.
- `agent_name_args` is still not backfilled into older configs (pre-existing).

## Follow-ups in the same branch

- **Review trees may post.** `outward` defaults on in review trees
  (`Config.ReviewOutward`, `review_outward: false` to withhold; a tree's own
  `outward` still wins). The default brief now posts with APPROVE or
  REQUEST_CHANGES.
- **Unreachable repos back off.** "Could not resolve to a Repository" is
  classified as `ErrRepoNotFound` and the branch is left alone for 6h
  (`Cache.MarkUnreachable`) instead of being retried every 30s.
- **watch.log rotates** at 1 MiB to one `.1` generation; raw stdio goes to
  `watch.out`.
- **No dump-layout against a dying session.** The watcher checks
  `zellij.SessionAlive` before its focused-tab query and before a launch.
