# Key Ceremony Runbook

How the offline `root` of the update metadata is created, signed and rotated (`SEC-121`,
`SEC-051`, [ADR-0054](../adr/0054-update-metadata-roles.md), plan p6 B11). The root names every key
that may sign for an environment and is signed by **2 of 3 offline keys**; it expires after
at most one year. The server never holds a root private key: it only stores and serves
roots that the holders signed on their own machines. The procedure below is rehearsed by
`TestRootCeremonyOnSoftHSM_SEC_121` (`backend/cmd/plux-server/metadata_softhsm_test.go`) against
SoftHSM2.

## Roles and material

- **Three key holders**, each with an HSM token (or, for the rehearsal, SoftHSM) holding one
  Ed25519 root key, and the `plux-pkcs11-helper` binary ([ADR-0060](../adr/0060-signing-backends-and-audit-checkpoints.md)).
  The PIN is given only through `PLUX_PKCS11_PIN`, never as a flag.
- **A coordinator**, who assembles the document and carries it between holders by file.
- **The server host**, which exports the online keys and uploads the signed root.

Root keys are Ed25519. A key reference is `pkcs11:object=<label>` with
`-pkcs11-socket <socket>`; `file:<name>` with `-key-dir` exists for development only.

## First root (2 of 3)

1. On each holder's offline machine, start the helper and export the public key:

   ```bash
   PLUX_PKCS11_PIN=… plux-pkcs11-helper --module /path/to/pkcs11.so --token-label <token> --socket /run/plux/pkcs11.sock &
   plux-server metadata root-key-export -key pkcs11:object=<label> -pkcs11-socket /run/plux/pkcs11.sock > holderN.pem
   ```

   The key ID is printed on standard error; each holder writes it down.
2. On the server host, export the environment's online keys (targets, snapshot,
   timestamp):

   ```bash
   plux-server metadata keys -config plux-server.yaml -organization <org> -environment <env> > online-keys.txt
   ```

3. The coordinator creates the unsigned root and compares the printed key IDs with the
   holders' notes:

   ```bash
   plux-server metadata root-new -type production -version 1 -expires 8760h -threshold 2 \
     -key holder1.pem -key holder2.pem -key holder3.pem -online-keys online-keys.txt -out root.json
   ```

   A production root needs a threshold of at least 2 and an expiry of at most 365 days.
4. Holder 1 signs on their machine; the command prints the SHA-256 of the signed part,
   which every holder compares out of band before signing:

   ```bash
   plux-server metadata root-sign -in root.json -out root.json -key pkcs11:object=<label> -pkcs11-socket /run/plux/pkcs11.sock
   ```

5. The file travels to a second holder, who runs the same command.
6. Check it: `plux-server metadata root-verify -in root.json` reports "2 of 2 required".
7. On the server host, upload it; the upload is audited (`update_metadata.root_uploaded`):

   ```bash
   plux-server metadata upload-root -config plux-server.yaml -organization <org> -environment <env> root.json
   ```

## Rotation (a new root, without an app release)

Rotate before the root expires, when a holder leaves, or when a root key is suspected
compromised. Devices accept a new root only if the previous root's threshold **and** the
new root's threshold signed it, and they follow the chain of roots in order (`SEC-051`).

1. New holders export their keys as in step 1; the coordinator re-runs `metadata keys` if
   the online keys changed.
2. `plux-server metadata root-new -type production -version 2 -expires 8760h -threshold 2 -key new1.pem -key new2.pem -key new3.pem -online-keys online-keys.txt -out root-v2.json`
3. Two holders of the **new** keys sign: `plux-server metadata root-sign -in root-v2.json -out root-v2.json -key <ref> -pkcs11-socket <socket>`.
4. Two holders of the **previous** keys sign with `-previous root-v1.json` added (without it
   their key is refused).
5. `plux-server metadata root-verify -in root-v2.json -previous root-v1.json` checks both
   thresholds.
6. Upload as in step 7. A root signed by only one side is refused with a "previous keys" or
   "new keys" threshold error.

## Online keys

The targets, snapshot and timestamp keys are online and per environment; the `worker`
re-signs the timestamp and snapshot before they expire. Rotating an online key needs a new
root that names the new key (the rotation above); no app release is needed.

## A lost or compromised key

- **One root key lost or compromised:** the remaining two holders still meet the threshold.
  Rotate at once (above), replacing that key; the new root no longer names it.
- **Two root keys compromised:** an attacker can sign a root, and a rotation cannot be
  trusted because the previous threshold is in the attacker's hands. Devices must be
  re-anchored: ship an app release with a new embedded root, and treat it as a security
  incident.
- **An online key compromised:** rotate the root to name a new online key; devices refuse
  metadata signed by the old key once they hold the new root.
