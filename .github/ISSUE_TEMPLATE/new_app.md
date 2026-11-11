---
name: New app request
about: Ask Midas to support another tool
labels: new-app
---

## App

- Name:
- Homepage:
- Version tested:
- Launch command:

## Data folders

List every folder the tool writes to, per OS (Windows / macOS / Linux).
Paths under the home directory only; files are not supported.

```
Linux:   ~/.example
macOS:   ~/.example
Windows: %USERPROFILE%\.example
```

## Is it safe to move these folders?

- Does the tool support a config-directory environment variable?
- Are any of these folders watched by the OS or another program?
- Which files are safe to omit from an `essential` backup?

## Process detection

Which executable should Midas look for to know the app is running? If it runs
on a shared runtime (node, python, electron), what substring appears in its
command line?
