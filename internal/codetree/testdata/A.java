package com.x;

import java.util.List;

/** Batch. */
public class Batch extends Base implements Doer {
    private int cap = 0;
    static final int A = 1, B = 2;

    public Batch(int c) { this.cap = c; }

    public boolean full() { return cap > 0; }

    enum Color { RED, GREEN }

    interface Inner { void run(); }
}

record Point(int x, int y) {}
