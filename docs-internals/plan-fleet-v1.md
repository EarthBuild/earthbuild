# A fleet worth turning on - v1

What E-F1 measured, turned into an order of work. The experiments are in
[plan-fleet-experiments.md](plan-fleet-experiments.md); this says what to build
and in which order, and nothing here is proposed without a measurement behind
it.

The one-sentence result: **the mechanism is sound and the bounds are wrong.**
Seven steps delegated across a LAN fetched their shared base once, spent 1.95s
moving 7.9 MiB against 13.886s of compute, and reported 87% compute-bound. That
is a fleet working. Everything below is about the cases where it does not get
that far.

## F1 - split liveness from completion

`Rendezvous.ask` gives a worker `defaultReach` = 10s, and `askOver` sets that
deadline on the stream it reads the **result** off. The constant's own comment
argues that "a live worker answers a control message in milliseconds", which is
true of a control message and false of an assignment: the worker fetches inputs
and runs the step before it replies.

So a step longer than ten seconds is indistinguishable from a dead machine.

The fix is not a larger constant - that reinstates E256, where a corpse in the
fleet cost a reach per step. It is an early acknowledgement: the worker answers
"taken" immediately, and liveness is then a heartbeat on the open stream while
completion has no deadline but the build's. A worker that stops heartbeating is
gone; a worker that is busy is busy.

**Blocks everything else.** Until it lands, no realistic step can be delegated
at all.

## F2 - a cold worker must be allowed to warm up

The same bound is what makes it absorbing rather than merely slow. A worker with
an empty store must fetch the base before it can start, a 1032 MiB base does not
cross a LAN inside ten seconds, and so the worker is dropped mid-fetch with its
store still empty - leaving the next assignment exactly as expensive. Measured:
`0 delegated, 6 local`, worker store 4.0K after the run.

`primeAll` already exists and already means "make sure every worker has what
this build stands on". Make it the path rather than an optimisation beside one:
priming is a long operation with its own lifetime, an assignment waits on the
prime for its inputs, and neither shares a clock with the other. F1 is what
makes that expressible.

## F3 - a step's platform must describe what it will run

`fromSpec` carries `opts.Platform` from the `FROM` line in front of it, so
`FROM +common` adopts nothing from `common`. The node is then labelled with the
driver's architecture while standing on a base of another, placement believes
the label, and a native worker for the real architecture is ruled ineligible.

Pinning every `FROM` by hand took one build from 1 delegated to 7. That is the
size of it, and asking authors to write `--platform` on every target is not a
fix - it is the workaround we used to get a measurement.

Two parts:

* a depending target inherits the platform of the target it stands on, unless it
  names one; and
* a diagnostic when a fleet holds workers no step is eligible for, because
  "0 delegated" currently reads as a placement defect and is not one.

## F4 - the blob plane belongs where the store is

The driver's keeper is `&fleet.Layers{Root: sb.StoreDir()}`, a host directory.
With the store inside the VM the layers are at `/var/lib/earthbuild/fast/store`
and that directory is not the store, so a layer a worker produced is fetched
somewhere no step can materialise from. E-F1 routed around it with
`EARTH_STORE_IN_VM=0`; a Mac cannot drive a fleet until it is fixed.

The shape is settled by everything else that has crossed this boundary. The
store *questions* moved into the guest one at a time - `StoreHas`, `StoreTree`,
`ViewDigests`, `WhyStaleIn`, the last of them worth 4.409s to 0.239s - and the
blob plane is the half that has not followed. `fleet.Keeper` is two methods.

Not the whole driver. `LOCALLY`, `SAVE ARTIFACT AS LOCAL`, secrets, the context
pack, the terminal, registry credentials and the fleet's own inbound endpoint
all face the host; moving the driver into the guest moves that boundary rather
than removing it, and it is the larger of the two.

## F5 - then, and only then, measure

E-F1's headline number is still unmeasured on a base worth moving: every run
that got far enough had a worker that already held it. With F1 and F2 in, repeat
it cold against the 1032 MiB base and record bytes moved per delegated step.
E-F6's ratchet goes on that number. E-F2's locality dispatch is the first thing
that should move it.

## What this does not touch

Correctness. I3 forbids a false hit, I4 gives no error variant, every blob is
verified against the name it was fetched under. None of the above weakens any of
it: F1 changes when a worker is believed dead, F2 when it is asked to do work,
F3 which machine is eligible, F4 which directory holds the bytes.
