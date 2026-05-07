const std = @import("std");

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    var stdout = std.Io.File.stdout().writer(init.io, &.{});
    var stderr = std.Io.File.stderr().writer(init.io, &.{});

    const argLen = args.len;
    if (argLen > 2) {
        try stderr.interface.print("too many arguments\n", .{});
        return;
    } else if (argLen < 2) {
        try stderr.interface.print("too few arguments\n", .{});
        return;
    }

    const to = try std.fmt.parseInt(usize, args[1], 10);

    for (1..to + 1) |num| {
        const result = try fizzBuzz(init.arena.allocator(), num);
        try stdout.interface.print("{s}\n", .{result});
    }
}

fn fizzBuzz(allocator: std.mem.Allocator, num: usize) ![]const u8 {
    if (num % 15 == 0) {
        return "fizzbuzz";
    } else if (num % 3 == 0) {
        return "fizz";
    } else if (num % 5 == 0) {
        return "buzz";
    } else {
        return try std.fmt.allocPrint(allocator, "{d}", .{num});
    }
}

test {
    var arena = std.heap.ArenaAllocator.init(std.testing.allocator);
    defer arena.deinit();

    try std.testing.expectEqualStrings("1", try fizzBuzz(arena.allocator(), 1));
    try std.testing.expectEqualStrings("2", try fizzBuzz(arena.allocator(), 2));
    try std.testing.expectEqualStrings("fizz", try fizzBuzz(arena.allocator(), 3));
    try std.testing.expectEqualStrings("buzz", try fizzBuzz(arena.allocator(), 5));
    try std.testing.expectEqualStrings("fizzbuzz", try fizzBuzz(arena.allocator(), 15));
}