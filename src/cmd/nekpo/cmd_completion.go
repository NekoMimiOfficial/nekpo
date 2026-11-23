package main

import (
	"errors"
	"fmt"
)

const completionBash = `_nekpo() {
  local cur cmd
  cur="${COMP_WORDS[COMP_CWORD]}"
  cmd="${COMP_WORDS[1]}"
  local commands="init build publish release install uninstall list outdated update deps app doctor run once keygen completion version help"
  if [ "$COMP_CWORD" -eq 1 ]; then
    COMPREPLY=($(compgen -W "$commands" -- "$cur"))
    return
  fi
  case "$cmd" in
    uninstall|update|outdated)
      COMPREPLY=($(compgen -W "$(nekpo list -q 2>/dev/null)" -- "$cur")) ;;
    deps)
      if [ "$COMP_CWORD" -eq 2 ]; then
        COMPREPLY=($(compgen -W "push pull sync lock list info remove" -- "$cur"))
      else
        COMPREPLY=($(compgen -f -- "$cur"))
      fi ;;
    app)
      if [ "$COMP_CWORD" -eq 2 ]; then
        COMPREPLY=($(compgen -W "list versions edit asset rm-asset rm-artifact rollback unpublish" -- "$cur"))
      else
        COMPREPLY=($(compgen -f -- "$cur"))
      fi ;;
    release)
      COMPREPLY=($(compgen -W "patch minor major" -- "$cur")) ;;
    completion)
      COMPREPLY=($(compgen -W "bash zsh fish" -- "$cur")) ;;
    install)
      COMPREPLY=($(compgen -W "gh+ store:" -- "$cur")) ;;
    init|build|publish)
      COMPREPLY=($(compgen -d -- "$cur")) ;;
    *)
      COMPREPLY=($(compgen -f -- "$cur")) ;;
  esac
}
complete -o default -F _nekpo nekpo
`

const completionZsh = `#compdef nekpo

_nekpo() {
  local -a commands
  commands=(
    'init:interactive project setup'
    'build:pull dependencies and run nekpo/build.sh'
    'publish:upload the project to the store'
    'release:bump the version, update the changelog, commit and tag'
    'install:install a package from git or the store'
    'uninstall:remove an installed package'
    'list:list installed packages'
    'outdated:check installed packages for newer versions'
    'update:update outdated packages'
    'deps:manage dependencies on the server'
    'app:manage apps on the store'
    'doctor:check the environment'
    'run:watch the repositories in builder.conf'
    'once:check the repositories once'
    'keygen:create a signing key'
    'completion:print a shell completion script'
    'version:print the version'
  )
  if (( CURRENT == 2 )); then
    _describe 'command' commands
    return
  fi
  case "$words[2]" in
    uninstall|update|outdated)
      compadd -- ${(f)"$(nekpo list -q 2>/dev/null)"} ;;
    deps)
      if (( CURRENT == 3 )); then compadd push pull sync lock list info remove; else _files; fi ;;
    app)
      if (( CURRENT == 3 )); then compadd list versions edit asset rm-asset rm-artifact rollback unpublish; else _files; fi ;;
    release)
      compadd patch minor major ;;
    completion)
      compadd bash zsh fish ;;
    init|build|publish)
      _files -/ ;;
    *)
      _files ;;
  esac
}

if [ "$funcstack[1]" = "_nekpo" ]; then
  _nekpo "$@"
else
  compdef _nekpo nekpo
fi
`

const completionFish = `set -l nekpo_cmds init build publish release install uninstall list outdated update deps app doctor run once keygen completion version help
complete -c nekpo -f
complete -c nekpo -n "not __fish_seen_subcommand_from $nekpo_cmds" -a "$nekpo_cmds"
complete -c nekpo -n "__fish_seen_subcommand_from uninstall update outdated" -a "(nekpo list -q 2>/dev/null)"
complete -c nekpo -n "__fish_seen_subcommand_from deps; and not __fish_seen_subcommand_from push pull sync lock list info remove" -a "push pull sync lock list info remove"
complete -c nekpo -n "__fish_seen_subcommand_from app; and not __fish_seen_subcommand_from list versions edit asset rm-asset rm-artifact rollback unpublish" -a "list versions edit asset rm-asset rm-artifact rollback unpublish"
complete -c nekpo -n "__fish_seen_subcommand_from release" -a "patch minor major"
complete -c nekpo -n "__fish_seen_subcommand_from completion" -a "bash zsh fish"
complete -c nekpo -n "__fish_seen_subcommand_from init build publish" -F
`

func cmdCompletion(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: nekpo completion bash|zsh|fish")
	}
	switch args[0] {
	case "bash":
		fmt.Print(completionBash)
	case "zsh":
		fmt.Print(completionZsh)
	case "fish":
		fmt.Print(completionFish)
	default:
		return fmt.Errorf("unsupported shell %q (bash, zsh or fish)", args[0])
	}
	return nil
}
