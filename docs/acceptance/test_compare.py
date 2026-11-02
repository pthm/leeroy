import unittest

from compare import episode_union_ms, summarize


class ComparisonTest(unittest.TestCase):
    def test_common_horizon_does_not_use_later_progress_or_invent_missing_signals(self):
        meta={"agent":"a","char":"Test","model":"model","runtime":"runtime"}
        samples=[
            {"elapsed_s":0,"level":5,"value":0,"signals":{"xp_earned":4800}},
            {"elapsed_s":60,"level":5,"value":0,"signals":{"xp_earned":4812}},
            {"elapsed_s":90,"level":6,"value":1,"signals":{"xp_earned":8000}},
        ]
        result=summarize((meta,{},samples),75)
        self.assertEqual(result["sample_elapsed_s"],60)
        self.assertEqual(result["signal_deltas"]["xp_earned"],12)
        self.assertEqual(result["goal_value"],0)
        self.assertIsNone(result["signal_deltas"]["deaths"])
        self.assertIsNone(result["full_run_diagnostics"])

    def test_overlapping_failure_episodes_are_not_double_counted(self):
        episodes=[{"start_ms":s,"end_ms":e} for s,e in [(20,40),(0,30),(5,10),(50,60)]]
        self.assertEqual(episode_union_ms(episodes),50)
        self.assertEqual(episode_union_ms([]),0)


if __name__=="__main__":
    unittest.main()
