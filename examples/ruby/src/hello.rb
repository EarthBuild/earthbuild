require 'colorize'

def hello
  'Hello'.colorize(:blue) + ' ' + 'earthbuild'.colorize(:green)
end

puts hello
