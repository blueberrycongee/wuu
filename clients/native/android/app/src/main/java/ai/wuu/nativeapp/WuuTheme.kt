package ai.wuu.nativeapp

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

@Composable internal fun WuuTheme(content: @Composable () -> Unit) {
    val colors = if (isSystemInDarkTheme()) darkColorScheme(
        primary = Color(0xFFD1F5E6), onPrimary = Color(0xFF142A23),
        primaryContainer = Color(0xFF284A3B), onPrimaryContainer = Color(0xFFD1F5E6),
        secondary = Color(0xFF9ED5BC), onSecondary = Color(0xFF142A23),
        secondaryContainer = Color(0xFF284237), onSecondaryContainer = Color(0xFFDAF2E6),
        tertiary = Color(0xFFAED1BF), onTertiary = Color(0xFF142A23),
        background = Color(0xFF171C1A), onBackground = Color(0xFFF0F4F2),
        surface = Color(0xFF171C1A), onSurface = Color(0xFFF0F4F2),
        onSurfaceVariant = Color(0xFFA8B5AE), surfaceVariant = Color(0xFF252E29),
        surfaceContainerLowest = Color(0xFF131815), surfaceContainerLow = Color(0xFF1D2520),
        surfaceContainer = Color(0xFF252E29), surfaceContainerHigh = Color(0xFF2B3630),
        surfaceContainerHighest = Color(0xFF334038), surfaceTint = Color.Transparent,
        outline = Color(0xFF7C8B82), outlineVariant = Color(0xFF36443B),
    ) else lightColorScheme(
        primary = Color(0xFF19251F), onPrimary = Color.White,
        primaryContainer = Color(0xFFD1F5E6), onPrimaryContainer = Color(0xFF192F24),
        secondary = Color(0xFF3B7058), onSecondary = Color.White,
        secondaryContainer = Color(0xFFE3F4EB), onSecondaryContainer = Color(0xFF203C2E),
        tertiary = Color(0xFF4E7662), onTertiary = Color.White,
        background = Color.White, onBackground = Color(0xFF19251F),
        surface = Color.White, onSurface = Color(0xFF19251F),
        onSurfaceVariant = Color(0xFF64736B), surfaceVariant = Color(0xFFF2F5F3),
        surfaceContainerLowest = Color.White, surfaceContainerLow = Color(0xFFF8FAF8),
        surfaceContainer = Color(0xFFF2F5F3), surfaceContainerHigh = Color(0xFFEBF0ED),
        surfaceContainerHighest = Color(0xFFE3EAE5), surfaceTint = Color.Transparent,
        outline = Color(0xFF7C8B82), outlineVariant = Color(0xFFDDE5DF),
    )
    MaterialTheme(colorScheme = colors, shapes = Shapes(
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
