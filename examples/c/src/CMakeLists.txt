cmake_minimum_required(VERSION 3.5.0)
project (c-example)
add_compile_options(-Wall -Wextra -pedantic -Werror)
add_executable(c-example main.c fibonacci.c)
