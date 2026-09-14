package ai.wuu.nativeapp

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

@Composable internal fun WuuTheme(content: @Composable () -> Unit) {
    val colors = if (isSystemInDarkTheme()) darkColorScheme(
        primary = Color(0xFFF2F2F2), onPrimary = Color(0xFF141414),
        primaryContainer = Color(0xFF2A2A2A), onPrimaryContainer = Color(0xFFF2F2F2),
        secondary = Color(0xFFBDBDBD), onSecondary = Color(0xFF141414),
        secondaryContainer = Color(0xFF2A2A2A), onSecondaryContainer = Color(0xFFF2F2F2),
        tertiary = Color(0xFFB0B0B0), onTertiary = Color(0xFF141414),
        background = Color(0xFF141414), onBackground = Color(0xFFF2F2F2),
        surface = Color(0xFF141414), onSurface = Color(0xFFF2F2F2),
        onSurfaceVariant = Color(0xFFA3A3A3), surfaceVariant = Color(0xFF242424),
        surfaceContainerLowest = Color(0xFF111111), surfaceContainerLow = Color(0xFF1C1C1C),
        surfaceContainer = Color(0xFF242424), surfaceContainerHigh = Color(0xFF2C2C2C),
        surfaceContainerHighest = Color(0xFF333333), surfaceTint = Color.Transparent,
        outline = Color(0xFF8A8A8A), outlineVariant = Color(0xFF3A3A3A),
    ) else lightColorScheme(
        primary = Color(0xFF1A1A1A), onPrimary = Color.White,
        primaryContainer = Color(0xFFF0F0F0), onPrimaryContainer = Color(0xFF1A1A1A),
        secondary = Color(0xFF5A5A5A), onSecondary = Color.White,
        secondaryContainer = Color(0xFFF2F2F2), onSecondaryContainer = Color(0xFF1A1A1A),
        tertiary = Color(0xFF6B6B6B), onTertiary = Color.White,
        background = Color.White, onBackground = Color(0xFF1A1A1A),
        surface = Color.White, onSurface = Color(0xFF1A1A1A),
        onSurfaceVariant = Color(0xFF6B6B6B), surfaceVariant = Color(0xFFF4F4F4),
        surfaceContainerLowest = Color.White, surfaceContainerLow = Color(0xFFF7F7F7),
        surfaceContainer = Color(0xFFF2F2F2), surfaceContainerHigh = Color(0xFFEBEBEB),
        surfaceContainerHighest = Color(0xFFE4E4E4), surfaceTint = Color.Transparent,
        outline = Color(0xFF8A8A8A), outlineVariant = Color(0xFFE5E5E5),
    )
    MaterialTheme(colorScheme = colors, typography = Typography(
        bodyLarge = Typography().bodyLarge.copy(fontSize = 15.sp, lineHeight = 22.sp),
        bodyMedium = Typography().bodyMedium.copy(fontSize = 14.sp, lineHeight = 20.sp),
        titleMedium = Typography().titleMedium.copy(fontSize = 16.sp, lineHeight = 22.sp),
    ), shapes = Shapes(
        extraSmall = RoundedCornerShape(8.dp), small = RoundedCornerShape(12.dp),
        medium = RoundedCornerShape(16.dp), large = RoundedCornerShape(24.dp),
        extraLarge = RoundedCornerShape(28.dp),
    ), content = content)
}

@Composable internal fun wuuFieldColors() = TextFieldDefaults.colors(
    focusedContainerColor = MaterialTheme.colorScheme.surfaceVariant,
    unfocusedContainerColor = MaterialTheme.colorScheme.surfaceVariant,
    disabledContainerColor = MaterialTheme.colorScheme.surfaceVariant,
    focusedIndicatorColor = Color.Transparent, unfocusedIndicatorColor = Color.Transparent,
    disabledIndicatorColor = Color.Transparent,
)
