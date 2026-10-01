These generated Go clients are copied unchanged from
agent-substrate/substrate v0.3.0, commit
`ccecc788a327dc11dcd6c21ee153f3d0cbb5cc97`, `pkg/proto/ateapipb`.
The upstream Apache 2.0 license is included here.

Keep this client pinned with the bundled chart. Copying the two generated files
avoids importing Substrate's server module and upgrading Obot's Kubernetes and
cloud dependencies solely for an RPC client.
