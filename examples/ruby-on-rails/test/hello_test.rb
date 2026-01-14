require 'test_helper'

class HelloTest < ActiveSupport::TestCase
  test "hello world" do
    assert_equal "Hello, World!", "Hello, World!"
  end

  test "Rails is working" do
    assert Rails.application.present?
    assert_equal Rails.env, "test"
  end
end
