tag: FIXED
date: 2026-11-10
`hmk` no longer prints control characters from names, titles and messages, so a shared Group's or another computer's name can't change your terminal or disguise a prompt; a single value a script reads, like `hmk group folder`, still prints exactly as it is into a pipe or a file. `hmk hooks install` keeps a settings file that's a symlink (into your dotfiles, say) a symlink.
