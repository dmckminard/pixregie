package agentcfg

import "errors"

// ErrNoToken means the agent has not been paired with a display yet.
var ErrNoToken = errors.New("no token stored")
