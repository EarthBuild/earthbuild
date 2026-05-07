const std = @import("std");

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());

    const argLen = args.len;
    if (argLen > 2) {
        std.debug.print("too many arguments\n", .{});
        return;
    } else if (argLen < 2) {
        std.debug.print("too few arguments\n", .{});
        return;
    }

    const to = try std.fmt.parseInt(usize, args[1], 10);

    for (0..to) |num| {
        const result = try fizzBuzz(init.arena.allocator(), num);
        std.debug.print("{s}\n", .{result});
    }
}

fn fizzBuzz(allocator: std.mem.Allocator, num: usize) ![]const u8 {
    if (num == 0) {
        return try std.fmt.allocPrint(allocator, "{d}", .{num});
    } else if (num % 15 == 0) {
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
    var gpa = std.heap.DebugAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    var arena = std.heap.ArenaAllocator.init(allocator);
    defer arena.deinit();

    try std.testing.expectEqualStrings("0", try fizzBuzz(arena.allocator(), 0));
    try std.testing.expectEqualStrings("fizz", try fizzBuzz(arena.allocator(), 3));
    try std.testing.expectEqualStrings("buzz", try fizzBuzz(arena.allocator(), 5));
    try std.testing.expectEqualStrings("fizzbuzz", try fizzBuzz(arena.allocator(), 15));
    try std.testing.expectEqualStrings("2", try fizzBuzz(arena.allocator(), 2));
}
