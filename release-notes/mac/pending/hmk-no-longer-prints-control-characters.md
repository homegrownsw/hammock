tag: FIXED
date: 2026-11-09
hmk no longer prints control characters from names, titles and messages it shows, so a shared Group's or another computer's name can't change your terminal (its clipboard, its screen) or disguise a prompt. A single value a script reads, like `hmk group folder`, is still printed exactly as it is when it goes to a pipe or a file.
