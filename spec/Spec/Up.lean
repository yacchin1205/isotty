import Spec.Steps

/-! The specification of `isotty up`: the steps of Spec/Steps.lean, in
their order, keep one another, and so one up that is not interrupted
reaches every step, whatever ups stopped part way before it. -/

/-- The steps of up, in order. -/
def templates : List (Template Config) := before ++ [restoreT] ++ after

/-- The order, checked on the steps' shapes by computation. -/
theorem shapes_ordered : (templates.map (·.shape)).Pairwise (fun a b => a.ordered b = true) := by
  decide

theorem shapes_spare : ∀ a ∈ templates.map (·.shape), ∀ b ∈ templates.map (·.shape), b.spares a = true := by
  decide

theorem rules (cfg : Config) : Rules (plan templates cfg) := plan_rules templates shapes_ordered shapes_spare cfg

theorem plain_parts : (before ++ after).all (·.plain) = true := by decide

theorem plan_mem {cfg : Config} {g : Sig Res} (hg : g ∈ plan templates cfg) :
    g.intent = [] ∨ ∃ p ∈ cfg.saved, g = restoreSig p := by
  simp only [plan, templates, List.flatMap_append, List.mem_append, List.flatMap_cons,
    List.flatMap_nil, List.append_nil] at hg
  have hp := plain_parts
  simp only [List.all_append, Bool.and_eq_true, List.all_eq_true] at hp
  rcases hg with (hg | hg) | hg
  · simp only [List.mem_flatMap] at hg
    obtain ⟨t, ht, hg⟩ := hg
    exact .inl (t.isPlain (hp.1 t ht) cfg g hg)
  · simp only [restoreT, List.mem_map, List.mem_filter, decide_eq_true_eq] at hg
    obtain ⟨p, ⟨_, hp'⟩, rfl⟩ := hg
    exact .inr ⟨p, hp', rfl⟩
  · simp only [List.mem_flatMap] at hg
    obtain ⟨t, ht, hg⟩ := hg
    exact .inl (t.isPlain (hp.2 t ht) cfg g hg)

/-- Where a home path the host saved exists, the saved home is in it. -/
def Restored (saved : List HomePath) (s : VM Res) : Prop :=
  ∀ p ∈ saved, s (.pathExists p) = true → s (.login p) = true

theorem restore_inv (p : HomePath) (s : VM Res) :
    (restoreSig p).InvA s ↔ (s (.pathExists p) = true → s (.login p) = true) := by
  simp [Sig.InvA, Sig.done, Sig.reached, Sig.observed, restoreSig]
  constructor
  · intro h he; exact (h he).2
  · intro h he; exact ⟨he, h he⟩

theorem inv_iff (cfg : Config) (s : VM Res) :
    (∀ a ∈ plan templates cfg, a.InvA s) ↔ Restored cfg.saved s := by
  constructor
  · intro h p hp
    refine (restore_inv p s).mp (h _ ?_)
    simp only [plan, templates, List.flatMap_append, List.mem_append, List.flatMap_cons,
      List.flatMap_nil, List.append_nil]
    refine .inl (.inr ?_)
    simp only [restoreT, List.mem_map, List.mem_filter, decide_eq_true_eq]
    exact ⟨p, ⟨by cases p <;> simp [allPaths], hp⟩, rfl⟩
  · intro h a ha
    rcases plan_mem ha with hn | ⟨p, hp, rfl⟩
    · exact inv_of_no_intent hn s
    · exact (restore_inv p s).mpr (h p hp)

/-- Ups that may each stop anywhere, under configurations that may differ
but share the host's saved homes, which only `down` writes. -/
inductive Ups (saved : List HomePath) : VM Res → VM Res → Prop
  | nil (s : VM Res) : Ups saved s s
  | cons {s t u : VM Res} (cfg : Config) : cfg.saved = saved →
      IUp (plan templates cfg) s t → Ups saved t u → Ups saved s u

theorem ups_restored {saved : List HomePath} {s t : VM Res} (h : Ups saved s t) :
    Restored saved s → Restored saved t := by
  induction h with
  | nil => exact id
  | cons cfg hc hi _ ih =>
    intro h0
    apply ih
    rw [← hc] at h0 ⊢
    exact (inv_iff cfg _).mp (interrupted_keeps (rules cfg) ((inv_iff cfg _).mpr h0) hi)

/-- A new VM has nothing. -/
def fresh : VM Res := fun _ => false

/-- From a new VM, after any number of ups stopped anywhere, with the
configuration changed between them in any way that keeps the host's saved
homes, one up that is not interrupted reaches every step of the
configuration of now. -/
theorem up_spec {saved : List HomePath} {t : VM Res} (h : Ups saved fresh t)
    (cfg : Config) (hc : cfg.saved = saved) :
    ∀ a ∈ plan templates cfg, a.reached (up (plan templates cfg) t) := by
  subst hc
  exact converges (rules cfg) ((inv_iff cfg t).mpr (ups_restored h (fun _ _ h => by simp [fresh] at h)))
