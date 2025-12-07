cmake_minimum_required(VERSION 3.5.0)
project (c-example-unit-test)
add_compile_options(-Wall -Wextra -pedantic -Werror)
add_executable(unit-test test.c ../src/fibonacci.c)
target_link_libraries(unit-test cunit)

