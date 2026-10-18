package interp

import (
	"strings"
	"testing"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

// Every Dockerfile instruction is either translated or refused by its own name.
//
// Not "most of them". Three times on this branch the same shape turned up - the
// mechanism present in this engine and the translation not connected to it -
// and each time it was found by somebody hitting it rather than by looking.
// SHELL cost a CI round; HEALTHCHECK and MAINTAINER would have cost the next
// two.
//
// A refusal that does not name the instruction is the other half: "not
// supported" against a line the reader has to guess at is what E68 is about.
//
// What it cannot tell apart is "translated" from "handled before translate is
// reached" - SHELL is intercepted by the builder and still refused here, and
// passes on the naming rule. Its own tests are what say it works; this one says
// nothing is silently dropped.
func TestEveryDockerfileInstructionIsTranslatedOrNamed(t *testing.T) {
	t.Parallel()

	const every = `ARG GLOBAL=x
FROM alpine:3.22 AS base
MAINTAINER someone@example.test
ARG GLOBAL
ENV A=b
LABEL k=v
USER root
WORKDIR /w
VOLUME /v
EXPOSE 80
SHELL ["/bin/sh", "-c"]
RUN true
CMD ["true"]
ENTRYPOINT ["true"]
HEALTHCHECK CMD true
COPY Dockerfile /d
ADD Dockerfile /a
STOPSIGNAL SIGTERM
ONBUILD RUN true
`

	ast, err := parser.Parse(strings.NewReader(every))
	if err != nil {
		t.Fatal(err)
	}

	stages, _, err := instructions.Parse(ast.AST)
	if err != nil {
		t.Fatal(err)
	}

	if len(stages) != 1 {
		t.Fatalf("the fixture parsed into %d stages", len(stages))
	}

	for _, instr := range stages[0].Commands {
		name := instructionName(instr)

		_, err := translate(instr, "Earthfile:1", map[string]string{"GLOBAL": "x"}, nil)
		if err == nil {
			continue
		}

		if !strings.Contains(err.Error(), name) {
			t.Errorf("%s is refused without naming itself: %v", name, err)
		}
	}
}
