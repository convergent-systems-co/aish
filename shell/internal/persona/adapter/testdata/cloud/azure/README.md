Azure sub-adapter fixture directory.

The azure sub-adapter shells out to `az account set --subscription`.
Test integration uses a stub `az` script generated at test runtime
(see cloud_test.go) — there is no pre-staged fixture script here
because the stub records argv to a tmp path the test creates.
