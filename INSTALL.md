# Installing the pgEdge CLI: Steps for an AI Agent

A user asked you to install the pgEdge CLI (`pgedge`) and its agent
skills on this machine. Follow these steps in order, and do not
improvise an alternative to any of them. Never use `sudo`.

Run every step even when `pgedge` is already installed. Each step
updates an older install and changes nothing on a current one.

If step 3 fails, stop and show the user the error. A refusal that step
3 tells you how to handle is not a failure. Steps 4 and 5 are
optional. If one fails, or the user declines it, tell the user why and
carry on with the next step. If a command reports that something is
missing, stop that step and tell the user what is missing. Do not
build a file by hand or edit code to work around it.

1. **Check the platform and the prerequisites.** Run `uname -s`.
   - It prints `Linux` or `Darwin`: you are on Linux or macOS. This
     includes WSL. Check that `curl` is on the PATH.
   - It prints a name starting with `MINGW`, `MSYS` or `CYGWIN`, or the
     command does not exist: you are on Windows. This includes Git
     Bash, where `install.sh` reports an unsupported platform, so never
     run it on Windows. Check that `npm` is on the PATH.
   - To check a command, run `command -v <name>`, or
     `Get-Command <name>` in PowerShell.
   - If `curl` is missing on Linux or macOS, or `npm` is missing on
     Windows, stop. Tell the user that step 3 needs it. On Windows,
     `npm` comes with Node.js 18 or newer (<https://nodejs.org>).
   - If `npx` is missing, tell the user that step 5 may need Node.js
     (<https://nodejs.org>), and carry on.
   - On Windows, read `~` and `$HOME` below as `%USERPROFILE%`. In
     PowerShell, run a quoted path with `&` in front, such as
     `& "<path>" version`, and use `curl.exe` where you need curl,
     because `curl` there is another command.
2. **Work out which agent you are:** Claude Code, Codex, Cursor or
   another agent. Step 5 depends on the answer.
3. **Install or update the CLI.** Check whether `pgedge` is on the
   PATH.
   - If it is not, on Windows, install it with npm:
     `npm install -g @pgedge/cli@beta`. Then check for `pgedge` again.
     If it is still missing, tell the user to add the directory that
     `npm prefix -g` prints to their PATH, and use `pgedge.cmd` in that
     directory as the binary's full path.
   - If it is not, on Linux or macOS, install the newest release with
     this command:
     `curl -fsSL https://raw.githubusercontent.com/pgEdge/pgedge-cli/main/install.sh | sh 2>&1 | cat`.
     Keep the `| cat`: it stops the script asking the user a question
     that you cannot answer. The script verifies the release before it
     installs it. Its `Installed pgedge <tag> to <path>` line names the
     binary's full path. If the output has no `Installed` line, the
     install failed.
   - If it is, update that binary with
     `"<path>" self update --force`. It verifies the newest release and
     swaps it in, or reports that `pgedge` is already up to date.
   - If `self update` refuses because Homebrew or npm owns the binary,
     run the command that the refusal names instead. When it names two
     npm tags, take `@beta` if the installed version has a `-beta`
     suffix.
   - If `self update` refuses because the binary is inside a git
     working tree, leave it. Tell the user to update that checkout
     themselves.

   Use the binary's full path for `pgedge` in the rest of these steps.
   If the script warns that the directory is not on the user's PATH,
   add the `export PATH=...` line it prints to the user's shell
   startup files. Skip a file that already has the line, and tell the
   user what you changed.
   - zsh: append it to `~/.zshrc`, creating the file if it is missing.
   - bash: append it to `~/.bashrc`, creating the file if it is
     missing. A login shell, the default in macOS Terminal, reads only
     the first of `~/.bash_profile`, `~/.bash_login` and `~/.profile`
     that exists. If that file does not mention `.bashrc`, append the
     line to it as well. If none of the three exists, create
     `~/.bash_profile` with the line.
4. **Set up shell completion.** Run
   `"<path>" completion install --force --write-rc </dev/null`. It
   rewrites the completion script from the current binary. For zsh
   and PowerShell, it also adds the line that loads the script, once.
   The `</dev/null` stops it asking a question that you cannot answer.
   - In PowerShell, run
     `& "<path>" completion install --shell powershell --force --write-rc`
     instead. PowerShell does not accept `</dev/null`, and the two
     flags already stop any question.
   - For bash, the script loads only where the bash-completion package
     is installed and loaded. Do not report bash completion as working.
     Tell the user that `complete -p -D` in a new bash shell prints a
     line when bash-completion is loaded. If it prints an error, they
     need to install that package and load it from their bash startup
     file.
5. **Install or update the agent skills.** Ask the user this question,
   and wait for the answer. Do not guess it: "Which pgEdge products do
   you use: pgEdge Starfleet, the Control Plane, both, or neither?"
   For neither, skip this step.

   Otherwise, take the first route below that matches, and only that
   one. A second route would give the agent a second copy of the
   skills.
   - **Claude Code, with the plugin installed.** Run
     `claude plugin list`. If it lists `pgedge@pgedge-cli`, run
     `claude plugin marketplace update pgedge-cli`, then
     `claude plugin update pgedge@pgedge-cli --scope <scope>`. Take
     `<scope>` from the scope that `claude plugin list` shows for the
     plugin. The plugin
     carries all five skills, pinned to the newest release.
   - **Codex, with the plugin installed.** If the directory
     `${CODEX_HOME:-$HOME/.codex}/plugins/cache/pgedge-cli/pgedge`
     exists, run `codex plugin marketplace upgrade pgedge-cli`, then
     `codex plugin add pgedge@pgedge-cli`. The plugin carries all five
     skills, pinned to the newest release.
   - **Any other case.** Install with the skills CLI for the user, so
     every project has the skills. If `npx` is missing, skip this step
     and tell the user it needs Node.js. A re-run replaces an older
     copy of each skill it names. If `npx -y skills list -g` already
     shows a `pgedge` skill that the command below leaves out, add
     `-s <skill-name>` for it, so that it is updated too.

     Pin the skills to the binary's release. Run `"<path>" version`.
     If its first line is a release version such as `0.5.1-beta.1`,
     `<tag>` is that version with a `v` in front. If the first line is
     anything else, such as `dev`, the binary was built from source:
     drop `#<tag>` from the command. Run the command for the user's answer:
     - pgEdge Starfleet:
       `npx -y skills add 'pgEdge/pgedge-cli#<tag>' -g -s pgedge -s pgedge-starfleet -s pgedge-byoc -s pgedge-managed -y`
     - Control Plane:
       `npx -y skills add 'pgEdge/pgedge-cli#<tag>' -g -s pgedge -s pgedge-controlplane -y`
     - Both: `npx -y skills add 'pgEdge/pgedge-cli#<tag>' -g -y`
6. **Check the install.** Run `"<path>" version` and `"<path>" doctor`,
   and show the user both outputs. A doctor warning about missing
   credentials is expected on a new install.
   - If step 5 used the skills CLI, run `npx -y skills list -g` and
     show the user its output.
   - Check for two copies. If `~/.agents/skills/pgedge/SKILL.md`
     exists, and so does
     `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/plugins/cache/pgedge-cli/pgedge`
     or the Codex directory from step 5, that agent may load the skills
     twice. Tell the user, and point them at
     <https://github.com/pgEdge/pgedge-cli/blob/main/docs/agent-skills.md>
     to remove one. Do not remove either copy yourself.
7. **Finish.** Tell the user these three things:
   - The skills load in a new agent session, or after
     `/reload-plugins` in Claude Code.
   - Each step you skipped, with the command that completes it later.
   - The next step: `pgedge starfleet auth login` for pgEdge Starfleet,
     or reading `pgedge llms controlplane` for the Control Plane.
