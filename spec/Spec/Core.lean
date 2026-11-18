/-! What `isotty up` is, apart from what its steps are: a list of steps,
each run unless what it observes says it is done. What up observes is told
apart from what a step means to reach, and an up may stop anywhere, a step
half done.

A step observes part of what it makes present (`ensures` minus `intent`).
The rest, its intent, is taken as present whenever the observation holds;
that is an invariant (`InvA`), and the order of the steps must keep it:
no step may make a later step's observation true behind its back. -/

variable {R : Type} [DecidableEq R]

abbrev VM (R : Type) := R → Bool

structure Sig (R : Type) where
  ensures : List R
  intent : List R := []
  clears : List R := []
  destroys : R → Bool := fun _ => false
  always : Bool := false
  atomic : Bool := false

namespace Sig

def observed (g : Sig R) : List R := g.ensures.filter (· ∉ g.intent)

def done (g : Sig R) (s : VM R) : Bool :=
  g.observed.all s && g.clears.all (fun r => !s r)

/-- What the step means to reach. -/
def reached (g : Sig R) (s : VM R) : Prop :=
  (∀ r ∈ g.ensures, s r = true) ∧ (∀ r ∈ g.clears, s r = false)

def run (g : Sig R) (s : VM R) : VM R := fun r =>
  if r ∈ g.ensures then true
  else if r ∈ g.clears || g.destroys r then false
  else s r

/-- The step stopped part way: some of its effects happened. -/
def runPart (g : Sig R) (sub : R → Bool) (s : VM R) : VM R := fun r =>
  if sub r then g.run s r else s r

def apply (g : Sig R) (s : VM R) : VM R :=
  if g.always || !g.done s then g.run s else s

/-- A step that observes less than it means is atomic, and clears nothing. -/
def wf (g : Sig R) : Prop :=
  (∀ r ∈ g.clears, r ∉ g.ensures) ∧ (∀ r ∈ g.intent, r ∈ g.ensures) ∧
  (g.intent ≠ [] → g.atomic = true ∧ g.clears = [])

/-- The observation stands for what the step means. -/
def InvA (g : Sig R) (s : VM R) : Prop := g.done s = true → g.reached s

end Sig

open Sig

/-- `b`, run after `a`, leaves what `a` reached in place. -/
def compat (a b : Sig R) : Prop :=
  (∀ r ∈ a.ensures, r ∉ b.clears ∧ b.destroys r = false) ∧ (∀ r ∈ a.clears, r ∉ b.ensures)

/-- `b` may make something present that `a` observes in place of what it
means. -/
def fools (a b : Sig R) : Prop := a.intent ≠ [] ∧ ∃ r ∈ b.ensures, r ∈ a.observed

/-- `b` never takes away what `a` only means. -/
def spares (b a : Sig R) : Prop := ∀ r ∈ a.intent, r ∉ b.clears ∧ b.destroys r = false

/-- `x` comes before `y`: `y` keeps what `x` reached, and `x` does not fool `y`. -/
def ordered (x y : Sig R) : Prop := compat x y ∧ ¬ fools y x

instance (x y : Sig R) : Decidable (ordered x y) := by
  unfold ordered compat fools; infer_instance

/-- What a step does when the up stops in it. -/
inductive Effect (g : Sig R) (s : VM R) : VM R → Prop
  | none : Effect g s s
  | run : Effect g s (g.run s)
  | part (sub : R → Bool) : g.atomic = false → Effect g s (g.runPart sub s)

/-- An up that may stop anywhere. -/
inductive IUp : List (Sig R) → VM R → VM R → Prop
  | stop (steps : List (Sig R)) (s : VM R) : IUp steps s s
  | part {g : Sig R} {rest s t} : Effect g s t → IUp (g :: rest) s t
  | next {g : Sig R} {rest s t} : IUp rest (g.apply s) t → IUp (g :: rest) s t

def up : List (Sig R) → VM R → VM R
  | [], s => s
  | g :: rest, s => up rest (g.apply s)

/-! ## Lemmas about one step's effect -/

theorem effect_cases {g : Sig R} {s t : VM R} (h : Effect g s t) (r : R) :
    t r = s r ∨ t r = g.run s r := by
  cases h with
  | none => exact .inl rfl
  | run => exact .inr rfl
  | part sub _ => unfold runPart; split <;> simp

theorem run_true {g : Sig R} {s : VM R} {r : R} (h : g.run s r = true) : r ∈ g.ensures ∨ s r = true := by
  unfold run at h
  split at h
  · exact .inl ‹_›
  · split at h
    · simp at h
    · exact .inr h

theorem keeps_reached {a b : Sig R} {s t : VM R} (hc : compat a b) (ha : a.reached s)
    (he : Effect b s t) : a.reached t := by
  refine ⟨fun r hr => ?_, fun r hr => ?_⟩
  · rcases effect_cases he r with h | h <;> rw [h]
    · exact ha.1 r hr
    · have ⟨h1, h2⟩ := hc.1 r hr
      unfold run
      split
      · rfl
      · simp [h1, h2, ha.1 r hr]
  · rcases effect_cases he r with h | h <;> rw [h]
    · exact ha.2 r hr
    · have h1 := hc.2 r hr
      unfold run
      simp [h1, ha.2 r hr]

theorem run_reached {g : Sig R} (hw : g.wf) (s : VM R) : g.reached (g.run s) := by
  refine ⟨fun r hr => by simp [run, hr], fun r hr => ?_⟩
  simp [run, hw.1 r hr, hr]

theorem inv_of_no_intent {g : Sig R} (h : g.intent = []) (s : VM R) : g.InvA s := by
  intro hd
  simp only [done, observed, h, Bool.and_eq_true, List.all_eq_true, List.mem_filter,
    List.not_mem_nil, not_false_eq_true, decide_true, and_true, Bool.not_eq_true'] at hd
  exact ⟨hd.1, hd.2⟩

/-- A step's effect keeps the invariant of a step after it that it does
not fool and spares. -/
theorem inv_later {a b : Sig R} {s t : VM R} (hw : a.wf) (hf : ¬ fools a b) (hs : spares b a)
    (hi : a.InvA s) (he : Effect b s t) : a.InvA t := by
  by_cases hn : a.intent = []
  · exact inv_of_no_intent hn t
  · have ⟨_, hcl⟩ := hw.2.2 hn
    intro hd
    -- what a observes at t was there at s: b makes none of it present
    have hobs : ∀ r ∈ a.observed, s r = true := by
      intro r hr
      have ht : t r = true := by
        simp only [done, Bool.and_eq_true, List.all_eq_true] at hd
        exact hd.1 r hr
      rcases effect_cases he r with h | h
      · rwa [h] at ht
      · rw [h] at ht
        rcases run_true ht with hb | hs'
        · exact absurd ⟨hn, r, hb, hr⟩ hf
        · exact hs'
    have hds : a.done s = true := by
      simp only [done, hcl, Bool.and_eq_true, List.all_eq_true, List.not_mem_nil]
      exact ⟨hobs, fun _ h => nomatch h⟩
    have hr := hi hds
    refine ⟨fun r hr' => ?_, fun r hr' => by simp [hcl] at hr'⟩
    by_cases hin : r ∈ a.intent
    · rcases effect_cases he r with h | h <;> rw [h]
      · exact hr.1 r hr'
      · have ⟨h1, h2⟩ := hs r hin
        unfold run
        split
        · rfl
        · simp [h1, h2, hr.1 r hr']
    · simp only [done, Bool.and_eq_true, List.all_eq_true] at hd
      exact hd.1 r (List.mem_filter.mpr ⟨hr', by simpa using hin⟩)

theorem inv_self {g : Sig R} {s t : VM R} (hw : g.wf) (hi : g.InvA s) (he : Effect g s t) : g.InvA t := by
  cases he with
  | none => exact hi
  | run => exact fun _ => run_reached hw s
  | part sub ha =>
    by_cases hn : g.intent = []
    · exact inv_of_no_intent hn _
    · simp [(hw.2.2 hn).1] at ha

theorem apply_effect (g : Sig R) (s : VM R) : Effect g s (g.apply s) := by
  unfold apply
  split
  · exact .run
  · exact .none

theorem apply_reached {g : Sig R} {s : VM R} (hw : g.wf) (hi : g.InvA s) : g.reached (g.apply s) := by
  unfold apply
  split
  · exact run_reached hw s
  · rename_i h
    simp at h
    exact hi h.2

/-! ## The invariant along an up -/

/-- Before the steps `rest`, with `pre` already applied in this up. -/
def Good (pre rest : List (Sig R)) (s : VM R) : Prop :=
  (∀ a ∈ pre ++ rest, a.InvA s) ∧ (∀ e ∈ pre, e.reached s)

structure Rules (steps : List (Sig R)) : Prop where
  wf : ∀ g ∈ steps, g.wf
  ordered : steps.Pairwise ordered
  spares : ∀ a ∈ steps, ∀ b ∈ steps, spares b a

theorem effect_inv {pre rest : List (Sig R)} {g : Sig R} {s t : VM R}
    (hr : Rules (pre ++ g :: rest)) (hg : Good pre (g :: rest) s) (he : Effect g s t) :
    ∀ a ∈ pre ++ g :: rest, a.InvA t := by
  have hp := hr.ordered
  rw [List.pairwise_append] at hp
  have hpre : ∀ x ∈ pre, ordered x g := fun x hx => hp.2.2 x hx g List.mem_cons_self
  have hrest : ∀ y ∈ rest, ordered g y := fun y hy => (List.pairwise_cons.mp hp.2.1).1 y hy
  intro a ha
  rcases List.mem_append.mp ha with ha' | ha'
  · exact fun _ => keeps_reached (hpre a ha').1 (hg.2 a ha') he
  · rcases List.mem_cons.mp ha' with rfl | ha''
    · exact inv_self (hr.wf a ha) (hg.1 a ha) he
    · exact inv_later (hr.wf a ha) (hrest a ha'').2
        (hr.spares a ha g (List.mem_append_right _ List.mem_cons_self)) (hg.1 a ha) he

theorem apply_good {pre rest : List (Sig R)} {g : Sig R} {s : VM R}
    (hr : Rules (pre ++ g :: rest)) (hg : Good pre (g :: rest) s) :
    Good (pre ++ [g]) rest (g.apply s) := by
  have hi := effect_inv hr hg (apply_effect g s)
  have hp := hr.ordered
  rw [List.pairwise_append] at hp
  refine ⟨fun a ha => hi a (by simpa using ha), fun e he => ?_⟩
  rcases List.mem_append.mp he with he' | he'
  · exact keeps_reached (hp.2.2 e he' g List.mem_cons_self).1 (hg.2 e he') (apply_effect g s)
  · simp at he'; subst he'
    exact apply_reached (hr.wf e (by simp)) (hg.1 e (by simp))

/-- Wherever an up stops, the invariant holds. -/
theorem iup_inv : ∀ {pre rest : List (Sig R)} {s t : VM R},
    Rules (pre ++ rest) → Good pre rest s → IUp rest s t → ∀ a ∈ pre ++ rest, a.InvA t
  | _, _, _, _, _, hg, .stop _ _ => hg.1
  | _, _, _, _, hr, hg, .part he => effect_inv hr hg he
  | pre, g :: rest, s, t, hr, hg, .next h => by
    have := iup_inv (pre := pre ++ [g]) (by simpa using hr) (apply_good hr hg) h
    simpa using this

/-- An up that is not interrupted reaches every step. -/
theorem up_reached : ∀ {pre rest : List (Sig R)} {s : VM R},
    Rules (pre ++ rest) → Good pre rest s → ∀ a ∈ pre ++ rest, a.reached (up rest s)
  | _, [], _, _, hg => by simpa [up] using hg.2
  | pre, g :: rest, s, hr, hg => by
    have := up_reached (pre := pre ++ [g]) (by simpa using hr) (apply_good hr hg)
    simpa [up] using this

/-- From a state where every observation stands for what it means, ups
stopped anywhere keep it so, and one up that is not interrupted then
reaches every step. -/
theorem converges {steps : List (Sig R)} (hr : Rules steps) {s : VM R}
    (hi : ∀ a ∈ steps, a.InvA s) : ∀ a ∈ steps, a.reached (up steps s) :=
  up_reached (pre := []) (by simpa using hr) ⟨by simpa using hi, by simp⟩

theorem interrupted_keeps {steps : List (Sig R)} (hr : Rules steps) {s t : VM R}
    (hi : ∀ a ∈ steps, a.InvA s) (h : IUp steps s t) : ∀ a ∈ steps, a.InvA t := by
  have := iup_inv (pre := []) (by simpa using hr) ⟨by simpa using hi, by simp⟩ h
  simpa using this
