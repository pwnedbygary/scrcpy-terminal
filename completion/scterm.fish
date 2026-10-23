# fish completion for scterm: its commands, its flags and their values, and
# paired devices. To use it, save it where fish looks for completions:
#     scterm completion fish > ~/.config/fish/completions/scterm.fish

# __scterm_args reports whether $argv[1] words come between scterm and the
# word being completed.
function __scterm_args
    test (count (commandline -opc)) -eq (math $argv[1] + 1)
end

# __scterm_command reports whether the first word after scterm is the
# command $argv[1], or with no argument, any command.
function __scterm_command
    set -l words (commandline -opc)
    if set -q argv[1]
        test "$words[2]" = "$argv[1]"
    else
        contains -- "$words[2]" {{join .Commands " "}}
    end
end

# __scterm_peers lists the paired devices for $argv[1]: peers (--peer) or
# forget. fish quotes each as it inserts it.
function __scterm_peers
    command scterm __complete $argv[1] 2>/dev/null
end

complete -c scterm -f
complete -c scterm -n '__scterm_args 0' -a '{{join .Commands " "}}'
complete -c scterm -n '__scterm_command forget; and __scterm_args 1' -a '(__scterm_peers forget)'
complete -c scterm -n '__scterm_command peers' {{fish .Peers}}
complete -c scterm -n '__scterm_command update' {{fish .Update}}
complete -c scterm -n '__scterm_command completion; and __scterm_args 1' -a '{{join .Shells " "}}'
complete -c scterm -n 'not __scterm_command' {{fish .Kind.bool}}
complete -c scterm -n 'not __scterm_command' {{fish .Kind.none}} -x
complete -c scterm -n 'not __scterm_command' {{fish .Kind.codec}} -x -a '{{join .Codecs " "}}'
complete -c scterm -n 'not __scterm_command' {{fish .Kind.source}} -x -a '{{join .Sources " "}}'
complete -c scterm -n 'not __scterm_command' {{fish .Kind.peer}} -x -a '(__scterm_peers peers)'
complete -c scterm -n 'not __scterm_command' {{fish .Kind.dir}} -x -a '(__fish_complete_directories)'
complete -c scterm -n 'not __scterm_command' {{fish .Kind.file}} -r -F
