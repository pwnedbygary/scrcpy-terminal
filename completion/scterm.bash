# bash completion for scterm: its commands, its flags and their values, and
# paired devices. To use it, add this line to ~/.bashrc:
#     source <(scterm completion bash)

_scterm() {
	local cur=${COMP_WORDS[COMP_CWORD]} prev=${COMP_WORDS[COMP_CWORD-1]} pre=
	COMPREPLY=()
	if (( COMP_CWORD > 1 )); then
		case ${COMP_WORDS[1]} in
		forget) (( COMP_CWORD == 2 )) && _scterm_peers "$cur" "" forget; return ;;
		peers) _scterm_words "$cur" "" '{{join .Peers " "}}'; return ;;
		update) _scterm_words "$cur" "" '{{join .Update " "}}'; return ;;
		completion) (( COMP_CWORD == 2 )) && _scterm_words "$cur" "" '{{join .Shells " "}}'; return ;;
		{{join .Commands "|"}}) return ;;
		esac
	fi
	# bash splits --flag=VALUE into three words, unless COMP_WORDBREAKS has
	# no =; then the word is whole, and pre is the part to keep.
	if [[ $cur == = ]]; then
		cur=
	elif [[ $prev == = ]] && (( COMP_CWORD > 1 )); then
		prev=${COMP_WORDS[COMP_CWORD-2]}
	elif [[ $cur == -*=* ]]; then
		prev=${cur%%=*} pre=${cur%%=*}= cur=${cur#*=}
	fi
	case $prev in
	{{either .Kind.codec}}) _scterm_words "$cur" "$pre" '{{join .Codecs " "}}' ;;
	{{either .Kind.source}}) _scterm_words "$cur" "$pre" '{{join .Sources " "}}' ;;
	{{either .Kind.peer}}) _scterm_peers "$cur" "$pre" peers ;;
	{{either .Kind.dir}}) compopt -o dirnames 2>/dev/null ;;
	{{either .Kind.file}}) compopt -o default 2>/dev/null ;;
	{{either .Kind.none}}) ;;
	*)
		if [[ $cur == -* ]]; then
			_scterm_words "$cur" "" '{{join .Flags " "}}'
		elif (( COMP_CWORD == 1 )); then
			_scterm_words "$cur" "" '{{join .Commands " "}}'
		fi
		;;
	esac
}

# _scterm_words offers each of the words in $3 that starts with $1, after $2.
_scterm_words() {
	local w
	for w in $3; do
		[[ $w == "$1"* ]] && COMPREPLY+=("$2$w")
	done
}

# _scterm_peers offers the paired devices for $1, the word typed so far,
# after $2, for $3: peers (--peer) or forget. Each candidate replaces the
# word as scterm writes it, quoted so that bash reads it as one word and runs
# nothing in it. In a whole --flag=VALUE word, readline replaces only what
# follows a quote in VALUE, so a candidate for the whole word cannot go in.
# Nor can one go in before text after the cursor, which readline keeps: the
# two could name another pairing.
_scterm_peers() {
	[[ -n $2 && $1 == *[\'\"]* ]] && return
	[[ ${COMP_LINE:COMP_POINT:1} == [^[:space:]] ]] && return
	local w
	while IFS= read -r w; do
		COMPREPLY+=("$2$w")
	done < <(command scterm __complete "$3" "$1" 2>/dev/null)
}

complete -F _scterm scterm
