/// Nothing depends on this crate, which is the point: editing it must not
/// recompile the ones that do not use it.
pub fn triangular(n: u64) -> u64 {
    n * (n + 1) / 2
}

#[cfg(test)]
mod tests {
    use super::triangular;

    #[test]
    fn it_sums() {
        assert_eq!(triangular(4), 10);
    }
}
