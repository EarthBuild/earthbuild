# Debugging techniques

Traditional debugging of errors during image builds often require a developer to place various print
commands through out the build commands to help reason about the state of the system before the failure occurs.
This can be slow and cumbersome.

EarthBuild provides an interactive mode which gives you access to a root shell when an error occurs, which we'll
cover in this guide.

Let's consider a test example that prints out a randomly generated phrase:

```Dockerfile
# Earthfile

VERSION 0.8
FROM python:3
WORKDIR /code

test:
  RUN curl https://raw.githubusercontent.com/jsvine/markovify/master/test/texts/sherlock.txt > /sherlock.txt
  COPY generate_phrase.py .
  RUN pip3 install markovify
  RUN python3 generate_phrase.py
```

and our python code:
```Python
# generate_phrase.py

import markovify
text = open('sherlock.txt').read()
text_model = markovify.Text(text)
print(text_model.make_sentence())
```


Now we can run it with `earth +test`, and we'll see a failure has occurred:

```
+test | --> RUN python3 generate_phrase.py
+test | Traceback (most recent call last):
+test |   File "/code/generate_phrase.py", line 2, in <module>
+test |     text = open('sherlock.txt').read()
+test |            ~~~~^^^^^^^^^^^^^^^^
+test | FileNotFoundError: [Errno 2] No such file or directory: 'sherlock.txt'
+test | ERROR Earthfile:9:3
+test |       The command
+test |           RUN python3 generate_phrase.py
+test |       did not complete successfully. Exit code 1

================================== ❌ FAILURE ===================================

...

Help: To debug your build, you can use the --interactive (-i) flag to drop into a shell of the failing RUN step: "earth -i +test"
```

Why can't it find the sherlock.txt file? Let's re-run `earth` with the `--interactive` (or `-i`) flag: `earth -i +test`

This time we see a slightly different message:

```
+test | --> RUN python3 generate_phrase.py
+test | Traceback (most recent call last):
+test |   File "/code/generate_phrase.py", line 2, in <module>
+test |     text = open('sherlock.txt').read()
+test |            ~~~~^^^^^^^^^^^^^^^^
+test | FileNotFoundError: [Errno 2] No such file or directory: 'sherlock.txt'
+test | earth debugger | Command /bin/sh -c 'python3 generate_phrase.py' failed with exit code 1
+test | Entering interactive debugger

root@buildkitsandbox:/code#
```

This time rather than exiting, earth will drop us into an interactive root shell within the container of the build environment.
This root shell will allow us to execute arbitrary commands within the container to figure out the problem:

```
root@buildkitsandbox:/code# ls
generate_phrase.py
root@buildkitsandbox:/code# find / -name sherlock.txt 2>/dev/null
/sherlock.txt
```

Ah ha! the corpus text file was located in the root directory rather than under `/code`. We can try moving it manually to see if that fixes the problem:

```
root@buildkitsandbox:/code# mv /sherlock.txt /code/.
root@buildkitsandbox:/code# python3 generate_phrase.py
As he spoke he picked up his chair and turned once more hurry back to her?
```

At this point we know what needs to be done to fix the test, so we can type exit (or ctrl-D), to exit the interactive shell.
The build then fails with the original error:

```
root@buildkitsandbox:/code# exit
exit
+test | ERROR Earthfile:9:3
+test |       The command
+test |           RUN python3 generate_phrase.py
+test |       did not complete successfully. Exit code 1
```

Note that even though we fixed the problem during debugging, the image will not have been saved, so we must go back to our Earthfile and fix the problem there:

```Dockerfile
# Earthfile

VERSION 0.8
FROM python:3
WORKDIR /code

test:
  RUN curl https://raw.githubusercontent.com/jsvine/markovify/master/test/texts/sherlock.txt > /code/sherlock.txt
  COPY generate_phrase.py .
  RUN pip3 install markovify
  RUN python3 generate_phrase.py
```


## Debugging integration tests

Let's consider a more complicated example where we are running integration tests within an embedded docker setup:

```Dockerfile
# Earthfile

VERSION 0.8

server:
  FROM python:3
  COPY server.py .

test:
  FROM earthbuild/dind:alpine-3.24-docker-29.8.2-r0
  RUN apk add --no-cache curl
  WITH DOCKER --load server:latest=+server
    RUN docker run --rm -d --network=host server:latest python3 server.py && sleep 5 && curl -s localhost:8000 | grep hello
  END

```

and our server.py code:

```Python
from http.server import HTTPServer, BaseHTTPRequestHandler

class SimpleHTTPRequestHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'Hello, world!')

httpd = HTTPServer(('localhost', 8000), SimpleHTTPRequestHandler)
httpd.serve_forever()
```

Let's fire up our integration test with `earth -P -i +test`:

```
+test | --> WITH DOCKER RUN  --privileged docker run --rm -d --network=host server:latest python3 server.py && sleep 5 && curl -s localhost:8000 | grep hello
+test | Loading images from BuildKit via embedded registry...
...
+test | Loading images done in 12830 ms
+test | 3d4140429fb62192dc93252b523dd8ebe472d9d4d184655ed3cad38bc8205f1a
+test | earth debugger | Command /bin/sh -c 'docker run --rm -d --network=host server:latest python3 server.py && sleep 5 && curl -s localhost:8000 | grep hello' failed with exit code 1
+test | Entering interactive debugger
/ #
```



There was a failure checking that the server output contained the string `hello`; let's see what is going on:


```
/ # docker ps -a
CONTAINER ID   IMAGE           COMMAND               CREATED              STATUS              PORTS     NAMES
3d4140429fb6   server:latest   "python3 server.py"   About a minute ago   Up About a minute             reverent_hypatia
```

The good news is our server container is running; let's see what happens when we try to connect to it:

```
/ # curl -s localhost:8000
Hello, world!/
```

Ah ha! The problem is our test is expecting a lowercase `h`, so we can fix our grep to look for an uppercase `H`:

```Dockerfile
# Earthfile

VERSION 0.8

server:
  FROM python:3
  COPY server.py .

test:
  FROM earthbuild/dind:alpine-3.24-docker-29.8.2-r0
  RUN apk add --no-cache curl
  WITH DOCKER --load server:latest=+server
    RUN docker run --rm -d --network=host server:latest python3 server.py && sleep 5 && curl -s localhost:8000 | grep Hello
  END
```

Then when we re-run our test we get:

```
+test | --> WITH DOCKER RUN  --privileged docker run --rm -d --network=host server:latest python3 server.py && sleep 5 && curl -s localhost:8000 | grep Hello
+test | Loading images from BuildKit via embedded registry...
...
+test | Loading images done in 13686 ms
+test | 7d13bfade6c1a10e4a0610f43876240f60663a4263a96c50e6573a9e7b747c2b
+test | Hello, world!
...
=========================== 🌍 Earth Build  ✅ SUCCESS ===========================
```

With the use of the interactive debugger; we were able to examine the state of the embedded containerized environment.

## Demo

[![asciicast](https://asciinema.org/a/361170.svg)](https://asciinema.org/a/361170?speed=2)

## Final tips

If you ever want to jump into an interactive debugging session at any point in your Earthfile, you can simply add a command that will fail such as:

```
  RUN false
```

and run earth with the `--interactive` (or `-i`) flag.

The debugger must be requested up front with `-i`; a `RUN` that failed without `-i` can't be attached to afterwards, so re-run the build with `-i` instead.


Hopefully you won't run into failures, but if you do the interactive debugger may help you discover the root cause more easily. Happy coding.
