# zsh completion for scterm: its commands, its flags and their values, and
# paired devices. To use it, add this line to ~/.zshrc, after compinit:
#     source <(scterm completion zsh)

_scterm() {
	local flag
	if (( CURRENT > 2 )); then
		case $words[2] in
		(forget) (( CURRENT == 3 )) && _scterm_peers forget; return ;;
		(peers) compadd -- {{join .Peers " "}}; return ;;
		(update) compadd -- {{join .Update " "}}; return ;;
		(completion) (( CURRENT == 3 )) && compadd -- {{join .Shells " "}}; return ;;
		({{join .Commands "|"}}) return ;;
		esac
	fi
	if [[ $PREFIX == -*=* ]]; then
		flag=${PREFIX%%=*}
		compset -P '*='
	else
		flag=$words[CURRENT-1]
	fi
	case $flag in
	({{either .Kind.codec}}) compadd -- {{join .Codecs " "}} ;;
	({{either .Kind.source}}) compadd -- {{join .Sources " "}} ;;
	({{either .Kind.peer}}) _scterm_peers peers ;;
	({{either .Kind.dir}}) _files -/ ;;
	({{either .Kind.file}}) _files ;;
	({{either .Kind.none}}) ;;
	(*)
		if [[ $PREFIX == -* ]]; then
			compadd -- {{join .Flags " "}}
		elif (( CURRENT == 2 )); then
			compadd -- {{join .Commands " "}}
		fi
		;;
	esac
}

# _scterm_peers offers the paired devices for $1: peers (--peer) or forget.
# Inside a quote that starts the word, zsh quotes each value it adds, and
# matches the word typed so far in any case, as scterm does. Otherwise each
# candidate is quoted already, as scterm writes it for the word typed so
# far, and replaces the whole word, a quote after --peer= included: zsh
# would leave a plain name bare, where it could name a global alias.
_scterm_peers() {
	local -a peers
	if [[ -n $compstate[quote] ]]; then
		peers=("${(@f)$(command scterm __complete $1 2>/dev/null)}")
		compadd -M 'm:{[:lower:][:upper:]}={[:upper:][:lower:]}' -- ${peers:#}
	else
		peers=("${(@f)$(command scterm __complete $1 "$PREFIX" 2>/dev/null)}")
		compadd -Q -U -- ${${peers:#}/#/$IPREFIX}
	fi
}

compdef _scterm scterm
