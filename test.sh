for submodule in github.com/EarthBuild/earthbuild/ast github.com/EarthBuild/earthbuild/util/deltautil; \
    do \
        for dep in $(go list -f '{{range .Deps}}{{.}} {{end}}' $submodule/...); \
        do \
            if [ "$(go list -f '{{if .Module}}{{.Module}}{{end}}' $dep)" == "github.com/EarthBuild/earthbuild" ]; \
            then \
               echo "FAIL: submodule $submodule imports $dep, which is in the core 'github.com/EarthBuild/earthbuild' module"; \
               exit 1; \
            fi; \
        done; \
    done
            if [ "$(go list -f '{{if .Module}}{{.Module}}{{end}}' $dep)" == "github.com/earthly/earthly" ]; \
            then \
               echo "FAIL: submodule $submodule imports $dep, which is in the core 'github.com/earthly/earthly' module"; \
               exit 1; \
            fi; \
        done; \
    done