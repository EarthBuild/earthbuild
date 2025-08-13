# Excluding patterns

When a build takes place, the `earthbuild` command sends any necessary local build contexts to the BuildKit daemon. In order to avoid sending unwanted files, you may exclude certain patterns by specifying an `.earthbuildignore` file.

The `.earthbuildignore` file must be present in the same directory as the target being built.

The syntax of the `.earthbuildignore` file is the same as the syntax of a [`.dockerignore` file](https://docs.docker.com/engine/reference/builder/#dockerignore-file). Behind the scenes, the matching is performed using the Go [`filepath.Match`](https://pkg.go.dev/path/filepath#Match) function.

Patterns of files to exclude from the build context are specified as one pattern per line, with empty lines or lines starting with `#` being ignored. Each pattern has the following syntax:

```
pattern:
	{ term }
term:
	'*'         matches any sequence of non-Separator characters
	'?'         matches any single non-Separator character
	'[' [ '^' ] { character-range } ']'
	            character class (must be non-empty)
	c           matches character c (c != '*', '?', '\\', '[')
	'\\' c      matches character c

character-range:
	c           matches character c (c != '\\', '-', ']')
	'\\' c      matches character c
	lo '-' hi   matches character c for lo <= c <= hi
```

{% hint style='info' %}
##### Note
Currently `.earthbuildignore` is only applied to local targets. If an `.earthbuildignore` file is specified within the context of a remote target, it will be silently ignored and exclusions would not take place.
{% endhint %}
