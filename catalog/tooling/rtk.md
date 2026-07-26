Run `rtk --version`. On success, all subsequent shell operations in every
workflow phase go through rtk (or the rtk hook rewrites them transparently).
On failure, WARN and proceed: rtk (token-optimized CLI proxy) was not found on
PATH — token costs will be higher; install rtk to reduce them.
