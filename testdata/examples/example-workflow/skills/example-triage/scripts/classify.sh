#!/bin/sh
# Classify a tracker issue with a typed decision (docs/contracts/decisions.md).
#
# Usage: classify.sh ISSUE_FILE
#
# Prints the triage process to run: bug, feature or question. Prints "ask"
# when the decision model is uncertain, refused or unavailable: the skill
# then asks the user instead of guessing. homonto never substitutes an
# answer, so the safe default lives here, in the plugin.
set -u
if [ "$#" -ne 1 ] || [ ! -r "$1" ]; then
	echo "usage: classify.sh ISSUE_FILE" >&2
	exit 2
fi
answer=$(homonto decide ${HOMONTO_CONFIG:+--config "$HOMONTO_CONFIG"} \
	--point example-workflow.classify --kind choice \
	--question "Which triage process fits this tracker issue? bug: something that worked is broken. feature: a request for new behavior. question: the reporter needs an answer, not a change." \
	--option bug --option feature --option question \
	--context-file "issue=$1" --format value)
if [ "$?" -eq 0 ] && [ -n "$answer" ]; then
	printf '%s\n' "$answer"
else
	echo ask
fi
