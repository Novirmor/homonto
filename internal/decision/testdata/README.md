# Decision protocol fixtures

Request bodies homonto must send (`*.request.json`) and provider responses
it must decode (`*.response.json`), one per question type, plus error and
refusal responses.

The response fixtures follow the shapes documented by OpenAI
(`developers.openai.com/api/docs/guides/decisions` and the create-decision
reference) and by OpenRouter for System One (`/api/alpha/decisions` schema,
Jev guide) as of 2026-11-03. They are not captured from paid calls. The
opt-in live smoke test (`HOMONTO_LIVE_DECISIONS`) checks them against a real
endpoint.
