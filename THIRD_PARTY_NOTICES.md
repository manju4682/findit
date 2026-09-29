# Third-party notices

FindIt is licensed under the Apache License 2.0 (see `LICENSE`). The macOS app
bundle also ships the following third-party components, unmodified, as separate
executables and dynamic libraries under `FindIt.app/Contents/Resources/engines`.
Their license files, where the upstream package provides them, are copied into
`engines/licenses/` at build time.

| Component | Used for | License | Source |
| --- | --- | --- | --- |
| The Sleuth Kit (`fls`, `icat`, `fsstat`, `mmls`) | Reading NTFS / FAT / exFAT filesystems | IBM Public License 1.0 and Common Public License 1.0 (some parts GPL-2.0); see upstream `licenses/` | https://github.com/sleuthkit/sleuthkit |
| libewf | Dependency of The Sleuth Kit | LGPL-3.0-or-later | https://github.com/libyal/libewf |
| AFFLIB | Dependency of The Sleuth Kit | BSD-style (see `COPYING`) | https://github.com/sshock/AFFLIBv3 |
| SQLite | Dependency of The Sleuth Kit | Public domain | https://sqlite.org |
| OpenSSL (`libcrypto`) | Dependency of AFFLIB | Apache-2.0 | https://www.openssl.org |

The engines are invoked as separate processes; FindIt does not link against
them. The dynamic libraries can be replaced with compatible builds in place.

Go and JavaScript dependencies compiled into FindIt itself are listed in
`go.mod` and `frontend/package.json` together with their licenses.
