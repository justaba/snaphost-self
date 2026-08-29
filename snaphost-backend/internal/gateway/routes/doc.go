// Package routes used to register the gateway's reverse-proxy routes to the
// downstream services. There is no downstream any more: the control plane and
// the Dockerfile generator are packages in this process, and cmd/snaphost
// registers their handlers on the same engine as the middleware chain.
//
// The package survives for rbac_policy_test.go, which loads the shipped Casbin
// files and asserts what each role may reach. That test guards a policy file,
// not a router, and it is the reason the gateway's authorisation cannot
// silently widen — including the check that no billing path is reachable by
// any role.
package routes
