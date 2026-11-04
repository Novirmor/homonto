---
name: ticket-to-delivery
description: Reference workflow — take a company ticket from intake through an installed engine, review and delivery, keeping the ticket's state and comments in step.
---

# ticket-to-delivery

The coordinator runs this workflow. It owns the ticket and the delivery; the
engine owns the change.

1. **Intake.** `ticket-cli view KEY` prints the ticket as JSON (`key`, `type`,
   `title`, `body`, `state`). A ticket already `done` ends here.
2. **Choose the process.** `homonto explain settings ticket-workflow --json`
   lists the configured processes. Use the one named after the ticket's
   `type`; when there is none, ask the user through the native question dialog
   which applies. Never invent steps.
3. **Hand to an engine.** Ask the coordinator to select one installed engine
   for the change, named after the ticket key in lowercase (`ABC-12` becomes
   `abc-12`), with the endpoint "candidate branch reviewed". Move the ticket:
   `ticket-cli transition KEY in-progress`.
4. **Review.** When the process sets `review = true`, delivery waits for the
   engine's reviewer to pass. Move the ticket to `in-review`.
5. **Deliver.** Read `delivery`. For `draft-pr`, open the draft through the
   installed hosting workflow's draft tools and publication flow. For
   `branch`, report the branch. Then comment the outcome on the ticket
   (`ticket-cli comment KEY --body-file <file>`) and move it to `done` only
   after the delivery is confirmed.

On resume, `homonto_status` shows the engine's change by name; continue from
the first step whose outcome is not recorded on the ticket.
