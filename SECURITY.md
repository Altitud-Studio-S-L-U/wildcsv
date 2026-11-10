# Security policy

wildcsv parses bytes that your program hands it. The library reads only from the `io.Reader` it is given, opens no files and no connections, and keeps no state between readers. The `wildcsv` command reads the file or standard input it is given and writes only to standard output, standard error and the file named by `--output`.

## Report a vulnerability

Use GitHub's **Report a vulnerability** option on this repository. If it is unavailable, contact [Altitud Studio](https://altitudstudio.ad/en/#contact) and ask for a private channel.

Do not put credentials, customer files or exploit details in a public issue. A synthetic reproduction, the affected version or commit and the potential impact help us investigate. Fixes are released in a new version of the latest release line.

## Supported versions

| Version | Security fixes |
| --- | --- |
| 0.x | Yes, in the latest 0.x release |

## Threat model

| Input | Trusted | How wildcsv handles it |
| --- | --- | --- |
| The bytes being read | No | Detection examines at most `Limits.SampleBytes` bytes. Reading holds one record at a time and fails a field, a record or a field count as soon as it exceeds its limit, so an unbalanced quote or a giant line never reaches memory whole. Every returned string is valid UTF-8. Errors carry positions, never input values. |
| `Options` | Chosen by your program | Validated by `Detect` and `NewReader`; invalid limits or dialects are rejected with `ErrInvalidOptions`. |
| `Options.Decode` and `Options.Warn` | Your code | Called as given. wildcsv still validates the UTF-8 that a custom decoder returns. |

A bug that lets input make wildcsv exceed a limit, panic, loop without consuming input or return invalid UTF-8 is a security bug: report it privately.
