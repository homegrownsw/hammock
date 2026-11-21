package com.homegrownsw.hammock.ui

import android.os.Build
import android.view.HapticFeedbackConstants
import android.view.View

// The taps you feel as an answer goes, or as a swipe reaches the point where
// letting go closes the row: the system's own, so they follow its
// touch-feedback setting.

/** Yes: Allow, Always, Let View or Drive. */
fun View.hapticConfirm() = performHapticFeedback(HapticFeedbackConstants.CONFIRM)

/** No: Deny, Decline. */
fun View.hapticReject() = performHapticFeedback(HapticFeedbackConstants.REJECT)

/** A swipe crossed the point where letting go acts. */
fun View.hapticThreshold() = performHapticFeedback(
    if (Build.VERSION.SDK_INT >= 34) HapticFeedbackConstants.GESTURE_THRESHOLD_ACTIVATE else HapticFeedbackConstants.CONTEXT_CLICK,
)
