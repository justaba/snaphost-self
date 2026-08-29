package shared

// SessionCookie is the name of the operator's session cookie.
//
// It lives here because two packages have to agree on it and neither owns the
// other: control/auth writes it on login, and the gateway's Auth middleware
// reads it on every request. Defining it twice would be a string that drifts
// silently — the failure being that nobody can log in, with no error anywhere
// saying why.
//
// Named for the product rather than the framework so it cannot collide with a
// cookie a deployed site sets. Deploys live on a different registrable domain
// for that reason, but the local development path puts them under one suffix.
const SessionCookie = "snaphost_session"
