use std::fmt;

/// Cap.
const CAP: usize = 10;
static NAME: &str = "x";

/// A batch.
#[derive(Debug)]
pub struct Batch<T> {
    pub cap: usize,
    items: Vec<T>,
}

pub enum Color { Red, Green(u8) }

pub trait Doer {
    fn run(&self);
}

impl<T> Batch<T> {
    pub fn full(&self) -> bool { self.cap > 0 }
}

impl fmt::Display for Batch<u8> {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result { Ok(()) }
}

mod inner {
    pub fn f() {}
}

macro_rules! m { () => {} }

type Id = u64;

fn main() {}
