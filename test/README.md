# test

Cross-component suites. Unit tests live next to the code they test.

| Directory | Contents | Arrives |
|---|---|---|
| [`e2e/`](e2e/README.md) | End-to-end tests on emulators, simulators and device farms (`QA-006`); the flows live with the app they drive, in [`apps/starter/integration_test`](../apps/starter/integration_test) | P3 |
| [`compat/`](compat/README.md) | Old-runtime/new-bundle compatibility matrix (`QA-010`) | P3 |
| `load/` | k6 load tests (`QA-007`) | P2 |
| [`bench/runtime/`](bench/runtime/README.md) | Runtime benchmark app in profile mode, and the device half of the sync benchmark (`QA-007`) | P3 |
| [`size/`](size/README.md) | Blank and Plux host apps for the size job (`RT-061`) | P3 |
| `security/` | DPoP, attestation and tampering suites (`QA-008`) | P6 |
| `layout-conformance/` | Plux Canvas vs. Flutter layout comparison (`STU-005`) | P11 |
