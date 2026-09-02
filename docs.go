package main

// Format example configs and regenerate registry documentation into docs/.
// Run via `make docs`; CI fails if docs/ is out of date.
//go:generate terraform fmt -recursive ./examples/
//go:generate go tool tfplugindocs generate --provider-name dataversecontact --rendered-provider-name "Dataverse Contact"
